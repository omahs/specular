package sequencer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/big"
	"time"

	ethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/specularl2/specular/clients/geth/specular/rollup/rpc/eth"
	"github.com/specularl2/specular/clients/geth/specular/rollup/types"
	"github.com/specularl2/specular/clients/geth/specular/rollup/types/da"
	"github.com/specularl2/specular/clients/geth/specular/utils/fmt"
	"github.com/specularl2/specular/clients/geth/specular/utils/log"
)

// Disseminates batches of L2 blocks via L1.
type batchDisseminator struct {
	cfg          Config
	batchBuilder BatchBuilder
	l1TxMgr      TxManager
	l2Client     L2Client
}

type unexpectedSystemStateError struct{ msg string }

func (e unexpectedSystemStateError) Error() string {
	return fmt.Sprintf("service in unexpected state: %s", e.msg)
}

type L2ReorgDetectedError struct{ err error }

func (e L2ReorgDetectedError) Error() string { return e.err.Error() }

func newBatchDisseminator(
	cfg Config,
	batchBuilder BatchBuilder,
	l1TxMgr TxManager,
) *batchDisseminator {
	return &batchDisseminator{cfg: cfg, batchBuilder: batchBuilder, l1TxMgr: l1TxMgr}
}

func (d *batchDisseminator) start(ctx context.Context, l2Client L2Client) error {
	d.l2Client = l2Client
	// Start with latest safe state.
	d.revertToFinalized()
	var ticker = time.NewTicker(d.cfg.SequencingInterval())
	defer ticker.Stop()
	d.step(ctx)
	for {
		select {
		case <-ticker.C:
			if err := d.step(ctx); err != nil {
				if errors.As(err, &unexpectedSystemStateError{}) {
					return fmt.Errorf("Aborting: %w", err)
				}
			}
		case <-ctx.Done():
			log.Info("Aborting.")
			return nil
		}
	}
}

// TODO: document
func (d *batchDisseminator) step(ctx context.Context) error {
	if err := d.appendToBuilder(ctx); err != nil {
		log.Error("Failed to append to batch builder", "error", err)
		if errors.As(err, &L2ReorgDetectedError{}) {
			log.Error("Reorg detected, reverting to safe state.")
			d.revertToFinalized()
		}
		return err
	}
	if err := d.sequenceBatches(ctx); err != nil {
		log.Error("Failed to sequence batch", "error", err)
		return err
	}
	return nil
}

// TODO: document
func (d *batchDisseminator) revertToFinalized() error {
	finalizedHeader, err := d.l2Client.HeaderByTag(context.Background(), eth.Finalized)
	if err != nil {
		return fmt.Errorf("Failed to get last finalized header: %w", err)
	}
	d.batchBuilder.Reset(types.NewBlockIDFromHeader(finalizedHeader))
	return nil
}

// Appends blocks to batch builder.
func (d *batchDisseminator) appendToBuilder(ctx context.Context) error {
	start, end, err := d.pendingL2BlockRange(ctx)
	if err != nil {
		return fmt.Errorf("Failed to get l2 block number: %w", err)
	}
	// TODO: this check might not be necessary
	// if start > end {
	// 	return &utils.L2ReorgDetectedError{Msg: fmt.Sprintf("start=%s exceeds end=%s", start, end)}
	// }
	for i := start; i <= end; i++ {
		block, err := d.l2Client.BlockByNumber(ctx, big.NewInt(0).SetUint64(i))
		if err != nil {
			return fmt.Errorf("Failed to get block: %w", err)
		}
		txs, err := encodeRLP(block.Transactions())
		if err != nil {
			return fmt.Errorf("Failed to encode txs: %w", err)
		}
		dBlock := da.NewDerivationBlock(block.NumberU64(), block.Time(), txs)
		err = d.batchBuilder.Append(dBlock, types.NewBlockRefFromHeader(block.Header()))
		if err != nil {
			if errors.As(err, &da.InvalidBlockError{}) {
				return L2ReorgDetectedError{err}
			}
			return fmt.Errorf("Failed to append block (num=%s): %w", i, err)
		}
	}
	return nil
}

// Determines first and last unsafe block numbers.
func (d *batchDisseminator) pendingL2BlockRange(ctx context.Context) (uint64, uint64, error) {
	var (
		lastAppended = d.batchBuilder.LastAppended()
		start        uint64
	)
	if lastAppended == types.EmptyBlockID {
		start = uint64(0) // TODO: genesis
	} else {
		start = lastAppended.GetNumber() + 1 // TODO: fix assumption
	}
	safe, err := d.l2Client.HeaderByTag(ctx, eth.Safe)
	if err != nil {
		return 0, 0, fmt.Errorf("Failed to get l2 safe header: %w", err)
	}
	if safe.Number.Uint64() > lastAppended.GetNumber() {
		// This should currently not be possible (single sequencer). TODO: handle restart case?
		return 0, 0, &unexpectedSystemStateError{msg: "Safe header exceeds last appended header"}
	}
	end, err := d.l2Client.BlockNumber(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("Failed to get most recent l2 block number: %w", err)
	}
	return start, end, nil
}

// Sequences batches until batch builder runs out (or signal from `ctx`).
func (d *batchDisseminator) sequenceBatches(ctx context.Context) error {
	for {
		// Non-blocking ctx check.
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := d.sequenceBatch(ctx); err != nil {
				if errors.Is(err, io.EOF) {
					log.Info("No more batches to sequence")
					return nil
				}
				return fmt.Errorf("Failed to sequence batch: %w", err)
			}
		}
	}
}

// Fetches a batch from batch builder and sequences to L1.
// Blocking call until batch is sequenced and N confirmations received.
// Note: this does not guarantee safety (re-org resistance) but should make re-orgs less likely.
func (d *batchDisseminator) sequenceBatch(ctx context.Context) error {
	// Construct tx data.
	batchAttrs, err := d.batchBuilder.Build()
	if err != nil {
		return fmt.Errorf("Failed to build batch: %w", err)
	}
	receipt, err := d.l1TxMgr.AppendTxBatch(
		ctx,
		batchAttrs.Contexts(),
		batchAttrs.TxLengths(),
		batchAttrs.FirstL2BlockNumber(),
		batchAttrs.TxBatch(),
	)
	if err != nil {
		return fmt.Errorf("Failed to send batch transaction: %w", err)
	}
	log.Info("Sequenced batch to L1", "first_block#", batchAttrs.FirstL2BlockNumber(), "tx_hash", receipt.TxHash)
	d.batchBuilder.Advance()
	return nil
}

func encodeRLP(txs ethTypes.Transactions) ([][]byte, error) {
	var encodedTxs [][]byte
	for _, tx := range txs {
		var txBuf bytes.Buffer
		if err := tx.EncodeRLP(&txBuf); err != nil {
			return nil, err
		}
		encodedTxs = append(encodedTxs, txBuf.Bytes())
	}
	return encodedTxs, nil
}
