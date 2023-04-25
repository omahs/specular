package sequencer

import (
	"context"
	"sync"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/specularl2/specular/clients/geth/specular/rollup/utils"
	"github.com/specularl2/specular/clients/geth/specular/rollup/utils/log"
)

type executor struct {
	cfg      SequencerServiceConfig
	backend  ExecutionBackend
	orderer  orderer
	l2Client L2Client
}

// This goroutine fetches, orders and executes txs from the tx pool.
// Commits an empty block if no txs are received within an interval
// TODO: handle reorgs in the decentralized sequencer case.
// TODO: commit a msg-passing tx in empty block.
func (e *executor) start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	// Watch transactions in TxPool
	txsCh := make(chan core.NewTxsEvent, 4096)
	txsSub := e.backend.SubscribeNewTxsEvent(txsCh)
	defer txsSub.Unsubscribe()
	batchCh := utils.SubscribeBatched(ctx, txsCh, e.cfg.Sequencer().MinExecutionInterval, e.cfg.Sequencer().MaxExecutionInterval)
	for {
		select {
		case evs := <-batchCh:
			var txs []*types.Transaction
			for _, ev := range evs {
				txs = append(txs, ev.Txs...)
			}
			if len(txs) == 0 {
				log.Info("No txs received in last execution window.")
				continue
			} else {
				var err error
				txs, err = e.orderer.OrderTransactions(ctx, txs)
				if err != nil {
					log.Crit("Failed to order txs", "err", err)
				}
			}
			if len(txs) == 0 {
				log.Info("No txs to execute post-ordering.")
				continue
			}
			err := e.backend.CommitTransactions(txs)
			if err != nil {
				log.Crit("Failed to commit txs", "err", err)
			}
			log.Info("Committed txs", "num_txs", len(txs))
		case <-ctx.Done():
			log.Info("Aborting.")
			return
		}
	}
}