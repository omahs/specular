package stage

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/beacon"
	ethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/specularl2/specular/clients/geth/specular/rollup/rpc/bridge"
	"github.com/specularl2/specular/clients/geth/specular/rollup/rpc/eth"
	"github.com/specularl2/specular/clients/geth/specular/rollup/services/api"
	"github.com/specularl2/specular/clients/geth/specular/rollup/types"
)

// Represents a stage in a pipeline.
// Generic parameters:
// `T`: Stage output type.
type StageOps[T any] interface {
	// Possible errors returned:
	// - RetryableError: Indicates caller should retry step.
	// - RecoverableError: Indicates caller should perform recovery.
	// - Unrecoverable fatal error (i.e. any other type): Unexpected. Indicates caller should not retry.
	Pull(ctx context.Context) (T, error)
	// Recovers from a re-org to the given L1 block number.
	Recover(ctx context.Context, l1BlockID types.BlockID) error
}

type ExecutionBackend interface {
	ForkchoiceUpdate(update *ForkChoiceState) (*ForkChoiceResponse, error)
	BuildPayload(payloadAttrs api.BuildPayloadAttributes) error
}

type ForkChoiceState = beacon.ForkchoiceStateV1
type ForkChoiceResponse = beacon.ForkChoiceResponse

type L1Client interface {
	HeaderByNumber(ctx context.Context, number *big.Int) (*ethTypes.Header, error)
	BlockByHash(ctx context.Context, hash common.Hash) (*ethTypes.Block, error)
}

type L2Client interface {
	EnsureDialed(ctx context.Context) error
	BlockNumber(ctx context.Context) (uint64, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*ethTypes.Header, error)
	HeaderByTag(ctx context.Context, tag eth.BlockTag) (*ethTypes.Header, error)
}

type L1State interface {
	Head() types.BlockID
	Safe() types.BlockID
	Finalized() types.BlockID
}

type RetryableError struct{ Err error }
type RecoverableError struct{ Err error }

func (e RetryableError) Error() string   { return e.Err.Error() }
func (e RecoverableError) Error() string { return e.Err.Error() }

type RollupState interface {
	OnAssertionCreated(ctx context.Context, l1BlockID types.BlockID, tx *ethTypes.Transaction) error
	OnAssertionConfirmed(ctx context.Context, l1BlockID types.BlockID, tx *ethTypes.Transaction) error
	OnAssertionRejected(ctx context.Context, l1BlockID types.BlockID, tx *ethTypes.Transaction) error
}

type DerivationConfig interface {
	L1Config
	GenesisConfig
}

type L1Config interface {
	GetChainID() uint64
	GetSequencerInboxAddr() common.Address
	GetRollupAddr() common.Address
}

type GenesisConfig interface {
	GetGenesisL1BlockID() types.BlockID
}

// L1HeaderRetrieval -> L1TxRetrieval -> L1TxProcessing (RollupState + PayloadBuilder) -> L2ForkChoiceUpdate
func CreatePipeline(
	cfg DerivationConfig,
	execBackend ExecutionBackend,
	rollupState RollupState,
	l2Client L2Client,
	l1Client L1Client,
	l1State L1State,
) *TerminalStage[types.BlockRelation] {
	// Define and chain stages together.
	var (
		// Initialize processors
		daHandlers, rollupTxHandlers = createProcessors(cfg, execBackend, rollupState, l2Client)
		// Initialize stages
		l1HeaderRetrievalStage = L1HeaderRetrievalStage{cfg.GetGenesisL1BlockID(), l1Client}
		l1TxRetrievalStage     = NewStage[types.BlockID, filteredBlock](
			&l1HeaderRetrievalStage,
			NewL1TxRetriever(l1Client, createTxFilterFn(daHandlers, rollupTxHandlers)),
		)
		l1TxProcessingStage = NewStage[filteredBlock, types.BlockRelation](
			l1TxRetrievalStage,
			NewL1TxProcessor(daHandlers, rollupTxHandlers),
		)
		l2ForkChoiceUpdateStage = NewTerminalStage[types.BlockRelation](
			l1TxProcessingStage,
			NewL2ForkChoiceUpdater(cfg, execBackend, l2Client, l1State),
		)
	)
	return l2ForkChoiceUpdateStage
}

func createProcessors(
	cfg L1Config,
	execBackend ExecutionBackend,
	rollupState RollupState,
	l2Client L2Client,
) (map[txFilterID]daSourceHandler, map[txFilterID]txHandler) {
	var (
		seqInboxABIMethods = bridge.InboxABIMethods()
		rollupABIMethods   = bridge.RollupABIMethods()
		payloadBuilder     = NewPayloadBuilder(cfg, execBackend, l2Client)
	)
	// Define handlers for l1 tx processing.
	daHandlers := map[txFilterID]daSourceHandler{
		{cfg.GetSequencerInboxAddr(), string(seqInboxABIMethods[bridge.AppendTxBatchFnName].ID)}: payloadBuilder.BuildPayloads,
	}
	rollupTxHandlers := map[txFilterID]txHandler{
		{cfg.GetRollupAddr(), string(rollupABIMethods[bridge.CreateAssertionFnName].ID)}:                 rollupState.OnAssertionCreated,
		{cfg.GetRollupAddr(), string(rollupABIMethods[bridge.ConfirmFirstUnresolvedAssertionFnName].ID)}: rollupState.OnAssertionConfirmed,
		{cfg.GetRollupAddr(), string(rollupABIMethods[bridge.RejectFirstUnresolvedAssertionFnName].ID)}:  rollupState.OnAssertionRejected,
	}
	return daHandlers, rollupTxHandlers
}

func createTxFilterFn(
	daSourceHandlers map[txFilterID]daSourceHandler,
	rollupTxHandlers map[txFilterID]txHandler,
) func(*ethTypes.Transaction) bool {
	// Function returns true iff the tx is of a type handled by either a da source or rollup tx handler.
	return func(tx *ethTypes.Transaction) bool {
		var to = tx.To()
		if to == nil {
			return false
		}
		var (
			addr     = *to
			methodID = tx.Data()[:bridge.MethodNumBytes]
			filterID = txFilterID{addr, string(methodID)}
		)
		if _, ok := daSourceHandlers[filterID]; ok {
			return true
		}
		if _, ok := rollupTxHandlers[filterID]; ok {
			return true
		}
		return false
	}
}
