package main

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"os"

	"github.com/urfave/cli/v2"
	"golang.org/x/sync/errgroup"

	"github.com/ethereum/go-ethereum/accounts"
	bind "github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/external"
	"github.com/ethereum/go-ethereum/common"
	ethTypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/specularL2/specular/services/sidecar/rollup/derivation"
	"github.com/specularL2/specular/services/sidecar/rollup/rpc/bridge"
	"github.com/specularL2/specular/services/sidecar/rollup/rpc/eth"
	"github.com/specularL2/specular/services/sidecar/rollup/rpc/eth/txmgr"
	"github.com/specularL2/specular/services/sidecar/rollup/services"
	"github.com/specularL2/specular/services/sidecar/rollup/services/disseminator"
	"github.com/specularL2/specular/services/sidecar/rollup/services/validator"
	"github.com/specularL2/specular/services/sidecar/utils/fmt"
	"github.com/specularL2/specular/services/sidecar/utils/log"
)

type serviceCfg interface {
	GetAccountAddr() common.Address
	GetPrivateKey() *ecdsa.PrivateKey
	GetClefEndpoint() string
	GetTxMgrCfg() txmgr.Config
}

func main() {
	app := &cli.App{
		Name:   "sidecar",
		Usage:  "launch a validator and/or disseminator",
		Action: startServices,
	}
	app.Flags = services.CLIFlags()
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Starts the CLI-specified services (blocking).
func startServices(cliCtx *cli.Context) error {
	// Configure logger.
	glogger := log.NewGlogHandler(log.StreamHandler(os.Stderr, log.TerminalFormat(false)))
	glogger.Verbosity(log.Lvl(cliCtx.Int(services.VerbosityFlag.Name)))
	log.Root().SetHandler(glogger)
	log.Info("Parsing configuration")
	cfg, err := services.ParseSystemConfig(cliCtx)
	if err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	var (
		disseminator *disseminator.BatchDisseminator
		validator    *validator.Validator
		eg, ctx      = errgroup.WithContext(context.Background())
	)
	log.Info("Starting l1 state sync...")
	l1State, err := createL1State(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to start syncing l1 state: %w", err)
	}
	if cfg.Disseminator().GetIsEnabled() {
		log.Info("Starting disseminator...")
		disseminator, err = createDisseminator(context.Background(), cfg, l1State)
		if err != nil {
			return fmt.Errorf("failed to create disseminator: %w", err)
		}
		if err := disseminator.Start(ctx, eg); err != nil {
			return fmt.Errorf("failed to start disseminator: %w", err)
		}
	}
	if cfg.Validator().GetIsEnabled() {
		log.Info("Starting validator...")
		validator, err = createValidator(context.Background(), cfg, l1State)
		if err != nil {
			return fmt.Errorf("failed to create validator: %w", err)
		}
		if err := validator.Start(ctx, eg); err != nil {
			return fmt.Errorf("failed to start validator: %w", err)
		}
	}
	log.Info("Services running.")
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("service failed while running: %w", err)
	}
	log.Info("Services stopped.")
	return nil
}

func createDisseminator(
	ctx context.Context,
	cfg *services.SystemConfig,
	l1State *eth.EthState,
) (*disseminator.BatchDisseminator, error) {
	l1TxMgr, err := createTxManager(ctx, "disseminator", cfg.L1().Endpoint, cfg.Protocol(), cfg.Disseminator())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize l1 tx manager: %w", err)
	}
	var (
		encoder      = derivation.NewBatchV0Encoder(cfg)
		batchBuilder = derivation.NewBatchBuilder(cfg, encoder)
		l2Client     = eth.NewLazilyDialedEthClient(cfg.L2().GetEndpoint())
	)
	return disseminator.NewBatchDisseminator(cfg.Disseminator(), batchBuilder, l1TxMgr, l1State, l2Client), nil
}

func createValidator(
	ctx context.Context,
	cfg *services.SystemConfig,
	l1State *eth.EthState,
) (*validator.Validator, error) {
	l1TxMgr, err := createTxManager(ctx, "validator", cfg.L1().Endpoint, cfg.Protocol(), cfg.Validator())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize l1 tx manager: %w", err)
	}
	l1Client, err := eth.DialWithRetry(ctx, cfg.L1().GetEndpoint())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize l1 client: %w", err)
	}
	l1BridgeClient, err := bridge.NewBridgeClient(l1Client, cfg.Protocol())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize l1 bridge client: %w", err)
	}
	l2Client := eth.NewLazilyDialedEthClient(cfg.L2().GetEndpoint())
	return validator.NewValidator(cfg.Validator(), l1TxMgr, l1BridgeClient, l1State, l2Client), nil
}

func createTxManager(
	ctx context.Context,
	name string,
	l1RpcUrl string,
	protocolCfg services.ProtocolConfig,
	serCfg serviceCfg,
) (*bridge.TxManager, error) {
	transactor, err := createTransactor(
		serCfg.GetAccountAddr(), serCfg.GetClefEndpoint(), serCfg.GetPrivateKey(), protocolCfg.GetL1ChainID(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize transactor: %w", err)
	}

	log.Info("created transactor for", "addr", transactor.From)

	l1Client, err := eth.DialWithRetry(ctx, l1RpcUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize l1 client: %w", err)
	}

	signer := func(ctx context.Context, address common.Address, tx *ethTypes.Transaction) (*ethTypes.Transaction, error) {
		return transactor.Signer(address, tx)
	}

	txMgrCfg := serCfg.GetTxMgrCfg()
	txMgrCfg.From = transactor.From

	return bridge.NewTxManager(txmgr.NewTxManager(log.New("service", name), txMgrCfg, l1Client, signer), protocolCfg)
}

// Creates a transactor for the given account address, either using a clef endpoint (preferred) or secret key.
func createTransactor(
	accountAddress common.Address,
	clefEndpoint string,
	secretKey *ecdsa.PrivateKey,
	chainID uint64,
) (*bind.TransactOpts, error) {
	if clefEndpoint != "" {
		clef, err := external.NewExternalSigner(clefEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize external signer from clef endpoint: %w", err)
		}
		return bind.NewClefTransactor(clef, accounts.Account{Address: accountAddress}), nil
	}
	log.Warn("No external signer specified, using geth signer")
	return bind.NewKeyedTransactorWithChainID(secretKey, new(big.Int).SetUint64(chainID))
}

func createL1State(ctx context.Context, cfg *services.SystemConfig) (*eth.EthState, error) {
	l1Client, err := eth.DialWithRetry(ctx, cfg.L1().GetEndpoint())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize l1 client: %w", err)
	}
	l1State := eth.NewEthState()
	l1Syncer := eth.NewEthSyncer(l1State)
	l1Syncer.Start(ctx, l1Client)
	return l1State, nil
}
