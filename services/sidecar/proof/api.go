// Copyright 2022, Specular contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proof

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// Backend interface provides the common API services (that are provided by
// both full and light clients) with access to necessary functions.
type Backend interface {
	HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error)
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error)
	BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error)
	BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error)
	GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error)
	RPCGasCap() uint64
	ChainConfig() *params.ChainConfig
	Engine() consensus.Engine
	ChainDb() ethdb.Database
	// StateAtBlock returns the state corresponding to the stateroot of the block.
	// N.B: For executing transactions on block N, the required stateRoot is block N-1,
	// so this method should be called with the parent.
	StateAtBlock(ctx context.Context, block *types.Block, reexec uint64, base *state.StateDB, checkLive, preferDisk bool) (*state.StateDB, error)
	StateAtTransaction(ctx context.Context, block *types.Block, txIndex int, reexec uint64) (core.Message, vm.BlockContext, *state.StateDB, error)
}

// ProverAPI is the collection of Specular one-step proof APIs.
type ProverAPI struct {
	backend Backend
}

// NewAPI creates a new API definition for the Specular one-step proof services.
func NewAPI(backend Backend) *ProverAPI {
	return &ProverAPI{backend: backend}
}

type chainContext struct {
	backend Backend
	ctx     context.Context
}

func (context *chainContext) Engine() consensus.Engine {
	return context.backend.Engine()
}

func (context *chainContext) GetHeader(hash common.Hash, number uint64) *types.Header {
	header, err := context.backend.HeaderByNumber(context.ctx, rpc.BlockNumber(number))
	if err != nil {
		return nil
	}
	if header.Hash() == hash {
		return header
	}
	header, err = context.backend.HeaderByHash(context.ctx, hash)
	if err != nil {
		return nil
	}
	return header
}

func createChainContext(backend Backend, ctx context.Context) core.ChainContext {
	return &chainContext{backend: backend, ctx: ctx}
}

func (api *ProverAPI) ProveTransaction(ctx context.Context, hash common.Hash, target common.Hash, config *ProverConfig) (hexutil.Bytes, error) {
	return hexutil.Bytes{}, nil
}

// APIs return the collection of RPC services the tracer package offers.
func APIs(backend Backend) []rpc.API {
	// Append all the local APIs and return
	return []rpc.API{
		{
			Namespace: "proof",
			Version:   "1.0",
			Service:   NewAPI(backend),
			Public:    false,
		},
	}
}
