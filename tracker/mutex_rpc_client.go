package tracker

import (
	"context"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"golang.org/x/time/rate"
)

const (
	rpcMethodLimitCount  = 40
	rpcMethodLimitWindow = 10 * time.Second
)

// JSON-RPC method names subject to the per-method request cap.
const (
	rpcMethodGetTransaction           = "getTransaction"
	rpcMethodGetSignaturesForAddress  = "getSignaturesForAddress"
	rpcMethodGetBlocks                = "getBlocks"
	rpcMethodGetBlock                 = "getBlock"
	rpcMethodGetBlockHeight           = "getBlockHeight"
)

type MutexRPCClient struct {
	rpcClient      *rpc.Client
	mu             sync.Mutex
	methodLimiters map[string]*rate.Limiter
}

func NewMutexRPCClient(rpcClient *rpc.Client) *MutexRPCClient {
	methodLimiters := map[string]*rate.Limiter{
		rpcMethodGetTransaction:          newRPCMethodLimiter(),
		rpcMethodGetSignaturesForAddress: newRPCMethodLimiter(),
		rpcMethodGetBlocks:               newRPCMethodLimiter(),
		rpcMethodGetBlock:                newRPCMethodLimiter(),
		rpcMethodGetBlockHeight:          newRPCMethodLimiter(),
	}

	return &MutexRPCClient{
		rpcClient:      rpcClient,
		methodLimiters: methodLimiters,
	}
}

func newRPCMethodLimiter() *rate.Limiter {
	return rate.NewLimiter(
		rate.Every(rpcMethodLimitWindow/rpcMethodLimitCount),
		rpcMethodLimitCount,
	)
}

func withRPCLimits[T any](
	c *MutexRPCClient,
	ctx context.Context,
	method string,
	call func() (T, error),
) (T, error) {
	var zero T

	if lim := c.methodLimiters[method]; lim != nil {
		if err := lim.Wait(ctx); err != nil {
			return zero, err
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return call()
}

func (c *MutexRPCClient) GetTransaction(
	ctx context.Context,
	txSig solana.Signature,
	opts *rpc.GetTransactionOpts,
) (*rpc.GetTransactionResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetTransaction, func() (*rpc.GetTransactionResult, error) {
		return c.rpcClient.GetTransaction(ctx, txSig, opts)
	})
}

func (c *MutexRPCClient) GetSignaturesForAddressWithOpts(
	ctx context.Context,
	account solana.PublicKey,
	opts *rpc.GetSignaturesForAddressOpts,
) ([]*rpc.TransactionSignature, error) {
	return withRPCLimits(c, ctx, rpcMethodGetSignaturesForAddress, func() ([]*rpc.TransactionSignature, error) {
		return c.rpcClient.GetSignaturesForAddressWithOpts(ctx, account, opts)
	})
}

func (c *MutexRPCClient) GetBlocks(
	ctx context.Context,
	startSlot uint64,
	endSlot *uint64,
	commitment rpc.CommitmentType,
) (rpc.BlocksResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetBlocks, func() (rpc.BlocksResult, error) {
		return c.rpcClient.GetBlocks(ctx, startSlot, endSlot, commitment)
	})
}

func (c *MutexRPCClient) GetBlockWithOpts(
	ctx context.Context,
	slot uint64,
	opts *rpc.GetBlockOpts,
) (*rpc.GetBlockResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetBlock, func() (*rpc.GetBlockResult, error) {
		return c.rpcClient.GetBlockWithOpts(ctx, slot, opts)
	})
}

func (c *MutexRPCClient) GetBlockHeight(
	ctx context.Context,
	commitment rpc.CommitmentType,
) (uint64, error) {
	return withRPCLimits(c, ctx, rpcMethodGetBlockHeight, func() (uint64, error) {
		return c.rpcClient.GetBlockHeight(ctx, commitment)
	})
}
