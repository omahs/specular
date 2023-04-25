package sequencer

import (
	"context"
	"errors"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/specularl2/specular/clients/geth/specular/rollup/services"
	"github.com/specularl2/specular/clients/geth/specular/rollup/utils/fmt"
	"github.com/specularl2/specular/clients/geth/specular/rollup/utils/log"
)

// TODO: Support:
// - PBS-style ordering: publicize current mempool and call remote engine API.
// - remote ordering +  DA in single call (some systems conflate these roles -- e.g. Espresso)
type orderer interface {
	OrderTransactions(ctx context.Context, txs []*types.Transaction) ([]*types.Transaction, error)
}

type ordererByFee struct {
	backend  ExecutionBackend
	l2Client L2Client
}

func (o *ordererByFee) OrderTransactions(ctx context.Context, txs []*types.Transaction) ([]*types.Transaction, error) {
	sortedTxs := o.backend.Prepare(txs)
	txs, err := o.sanitize(ctx, sortedTxs)
	if err != nil {
		return nil, fmt.Errorf("Failed to sanitize txs: %w", err)
	}
	return txs, nil
}

func (o *ordererByFee) sanitize(
	ctx context.Context,
	sortedTxs services.TransactionsByPriceAndNonce,
) ([]*types.Transaction, error) {
	var sanitizedTxs []*types.Transaction
	for {
		tx := sortedTxs.Peek()
		if tx == nil {
			break
		}
		err := o.validateTx(ctx, tx)
		if errors.Is(err, &txValidationError{}) {
			log.Warn("Dropping tx", "tx", tx.Hash(), "err", err)
			sortedTxs.Pop()
			continue
		} else if err != nil {
			return nil, fmt.Errorf("Sanitization failed: %w", err)
		}
		sanitizedTxs = append(sanitizedTxs, tx)
		sortedTxs.Pop()
	}
	return sanitizedTxs, nil
}

func (o *ordererByFee) validateTx(ctx context.Context, tx *types.Transaction) error {
	// Check if tx exists on the L2 chain (TODO: is this really necessary)
	prevTx, _, err := o.l2Client.TransactionByHash(ctx, tx.Hash())
	if err != nil {
		return fmt.Errorf("Failed to query for tx by hash: %w", err)
	}
	if prevTx != nil {
		return &txValidationError{"tx already exists on-chain"}
	}
	return nil
}