package services

import (
	"context"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/specularl2/specular/clients/geth/specular/bindings"
	"github.com/specularl2/specular/clients/geth/specular/proof"
	"github.com/specularl2/specular/clients/geth/specular/rollup/client"
	"github.com/specularl2/specular/clients/geth/specular/rollup/derivation"
	"github.com/specularl2/specular/clients/geth/specular/rollup/rpc/bridge"
	"github.com/specularl2/specular/clients/geth/specular/rollup/rpc/eth"
	"github.com/specularl2/specular/clients/geth/specular/rollup/services/api"
	"github.com/specularl2/specular/clients/geth/specular/utils/fmt"
)

// TODO: delete.
type BaseService struct {
	Config BaseConfig

	Eth          api.ExecutionBackend
	ProofBackend proof.Backend
	L1Client     client.L1BridgeClient
	L1Syncer     *eth.EthSyncer

	Cancel context.CancelFunc
	Wg     sync.WaitGroup
}

type BaseConfig interface {
	GetRollupGenesisBlock() uint64
	GetAccountAddr() common.Address
}

func NewBaseService(execBackend api.ExecutionBackend, proofBackend proof.Backend, l1Client client.L1BridgeClient, cfg BaseConfig) (*BaseService, error) {
	return &BaseService{
		Config:       cfg,
		Eth:          execBackend,
		ProofBackend: proofBackend,
		L1Client:     l1Client,
		L1Syncer:     eth.NewEthSyncer(eth.NewEthState()),
	}, nil
}

// Starts the rollup service.
func (b *BaseService) Start(ctx context.Context, eg api.ErrGroup) error {
	b.L1Syncer.Start(ctx, b.L1Client)
	return nil
}

func (b *BaseService) Chain() *core.BlockChain {
	return b.Eth.BlockChain()
}

// Sync to current L1 block head and commit blocks.
// `start` is the block number to start syncing from.
// Returns the last synced block number (inclusive).
func (b *BaseService) SyncL2ChainToL1Head(ctx context.Context, start uint64) (uint64, error) {
	l1BlockHead, err := b.L1Client.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("Failed to sync to L1 head, err: %w", err)
	}
	opts := bind.FilterOpts{Start: start, End: &l1BlockHead, Context: ctx}
	eventsIter, err := b.L1Client.FilterTxBatchAppendedEvents(&opts)
	if err != nil {
		return 0, fmt.Errorf("Failed to sync to L1 head, err: %w", err)
	}
	err = b.processTxBatchAppendedEvents(ctx, eventsIter)
	if err != nil {
		return 0, fmt.Errorf("Failed to sync to L1 head, err: %w", err)
	}
	log.Info(
		"Synced L1->L2",
		"l1 start", start,
		"l1 end", l1BlockHead,
		"l2 size", b.Chain().CurrentBlock().Number(),
	)
	return l1BlockHead, nil
}

func (b *BaseService) SyncLoop(ctx context.Context, start uint64, newBatchCh chan<- struct{}) {
	defer b.Wg.Done()
	// Start watching for new TxBatchAppended events.
	subCtx, cancel := context.WithCancel(ctx)
	batchEventCh := client.SubscribeHeaderMapped[*bindings.ISequencerInboxTxBatchAppended](
		subCtx, b.L1Syncer.LatestHeaderBroker, b.L1Client.FilterTxBatchAppendedEvents, start,
	)
	defer cancel()
	// Process TxBatchAppended events.
	for {
		select {
		case ev := <-batchEventCh:
			log.Info("Processing `TxBatchAppended` event", "l1Block", ev.Raw.BlockNumber)
			err := b.processTxBatchAppendedEvent(ctx, ev)
			if err != nil {
				log.Crit("Failed to process event", "err", err)
			}
			if newBatchCh != nil {
				newBatchCh <- struct{}{}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (b *BaseService) processTxBatchAppendedEvents(
	ctx context.Context,
	eventsIter *bindings.ISequencerInboxTxBatchAppendedIterator,
) error {
	for eventsIter.Next() {
		err := b.processTxBatchAppendedEvent(ctx, eventsIter.Event)
		if err != nil {
			return fmt.Errorf("Failed to process event, err: %w", err)
		}
	}
	if err := eventsIter.Error(); err != nil {
		return fmt.Errorf("Failed to iterate through events, err: %w", err)
	}
	return nil
}

// Reads tx data associated with batch event and commits as blocks on L2.
func (b *BaseService) processTxBatchAppendedEvent(
	ctx context.Context,
	ev *bindings.ISequencerInboxTxBatchAppended,
) error {
	tx, _, err := b.L1Client.TransactionByHash(ctx, ev.Raw.TxHash)
	if err != nil {
		return fmt.Errorf("Failed to get transaction associated with TxBatchAppended event, err: %w", err)
	}
	in, err := bridge.UnpackAppendTxBatchInput(tx)
	if err != nil {
		return fmt.Errorf("Failed to decode transaction associated with TxBatchAppended event, err: %w", err)
	}
	blocks, err := derivation.BlocksFromData(in)
	if err != nil {
		return fmt.Errorf("Failed to split AppendTxBatch input into blocks, err: %w", err)
	}
	log.Info("Decoded blocks", "#blocks", len(blocks))
	localBlockNumber := b.Eth.BlockChain().CurrentBlock().NumberU64()
	// Commit only blocks ahead of the current L2 chain.
	for i := range blocks {
		if blocks[i].BlockNumber() > localBlockNumber {
			blocks = blocks[i:]
			b.commitBlocks(blocks)
			break
		}
	}

	return nil
}

// commitBlocks executes and commits sequenced blocks to local blockchain
// TODO: this function shares a lot of codes with Batcher
// TODO: use StateProcessor::Process() instead
func (b *BaseService) commitBlocks(blocks []derivation.DerivationBlock) error {
	firstL2BlockNumber := blocks[0].BlockNumber()
	log.Trace(
		"Committing blocks",
		"#firstL2BlockNumber", firstL2BlockNumber,
		"#numBlocks", len(blocks),
	)

	if len(blocks) == 0 {
		log.Warn("Commited empty list of blocks")
		return nil
	}

	chainConfig := b.Chain().Config()
	parent := b.Chain().CurrentBlock()

	if parent == nil {
		log.Warn("No parent block found")
		return fmt.Errorf("missing parent")
	}
	expectedParentNum := firstL2BlockNumber - 1
	if expectedParentNum != parent.NumberU64() {
		log.Warn("Parent block number mismatch", "#parentBlock", parent, "#firstL2BlockNumber", firstL2BlockNumber)
		return fmt.Errorf("rollup services unsynced")
	}
	state, err := b.Chain().StateAt(parent.Root())
	if err != nil {
		log.Warn("Could not read parent state")
		return err
	}
	state.StartPrefetcher("rollup")
	defer state.StopPrefetcher()

	currentBlockNumber := firstL2BlockNumber
	for _, sblock := range blocks {
		log.Trace("Reconstructing block header", "#blockNumber", currentBlockNumber)
		header := &types.Header{
			ParentHash: parent.Hash(),
			Number:     new(big.Int).SetUint64(currentBlockNumber),
			GasLimit:   core.CalcGasLimit(parent.GasLimit(), ethconfig.Defaults.Miner.GasCeil), // TODO: this may cause problem if the gas limit generated on sequencer side mismatch with this one
			Time:       sblock.Timestamp(),
			Coinbase:   b.Config.GetAccountAddr(),
			Difficulty: common.Big1, // Fake difficulty. Avoid use 0 here because it means the merge happened
		}
		gasPool := new(core.GasPool).AddGas(header.GasLimit)
		var receipts []*types.Receipt

		var txs []*types.Transaction
		for i, tx := range sblock.Txs() {
			err := txs[i].UnmarshalBinary(tx)
			if err != nil {
				return err
			}
		}

		for idx, tx := range txs {
			state.Prepare(tx.Hash(), idx)
			addr := b.Config.GetAccountAddr()
			receipt, err := core.ApplyTransaction(
				chainConfig, b.Chain(), &addr, gasPool, state, header, tx, &header.GasUsed, *b.Chain().GetVMConfig())
			if err != nil {
				log.Warn("Error while applying transactions", "#err", err)
				return err
			}
			receipts = append(receipts, receipt)
		}

		// Finalize header
		header.Root = state.IntermediateRoot(b.Chain().Config().IsEIP158(header.Number))
		header.UncleHash = types.CalcUncleHash(nil)
		// Assemble block
		block := types.NewBlock(header, txs, nil, receipts, trie.NewStackTrie(nil))
		hash := block.Hash()
		// Finalize receipts and logs
		var logs []*types.Log
		for i, receipt := range receipts {
			// Add block location fields
			receipt.BlockHash = hash
			receipt.BlockNumber = block.Number()
			receipt.TransactionIndex = uint(i)

			// Update the block hash in all logs since it is now available and not when the
			// receipt/log of individual transactions were created.
			for _, log := range receipt.Logs {
				log.BlockHash = hash
			}
			logs = append(logs, receipt.Logs...)
		}
		_, err := b.Chain().WriteBlockAndSetHead(block, receipts, logs, state, true)
		if err != nil {
			log.Warn("Error while writing new block", "#err", err)
			return err
		}
		parent = block

		// Only increment currentBlockNumber after block and header have been written to chain
		currentBlockNumber += 1
	}

	log.Trace(
		"All blocks have been written",
		"Chain height after write", b.Chain().CurrentBlock().NumberU64(),
	)

	return nil
}
