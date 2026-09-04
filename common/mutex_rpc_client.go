package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"golang.org/x/time/rate"
)

const (
	rpcMethodLimitCount                        = 30
	rpcMethodGetSignaturesForAddressLimitCount = 20
	rpcMethodLimitWindow                       = 10 * time.Second
)

// JSON-RPC method names subject to the per-method request cap.
const (
	rpcMethodGetBalance              = "getBalance"
	rpcMethodGetTokenAccountBalance  = "getTokenAccountBalance"
	rpcMethodGetAccountInfo          = "getAccountInfo"
	rpcMethodGetProgramAccounts      = "getProgramAccounts"
	rpcMethodGetLatestBlockhash      = "getLatestBlockhash"
	rpcMethodGetSlot                 = "getSlot"
	rpcMethodSendTransaction         = "sendTransaction"
	rpcMethodGetSignatureStatuses    = "getSignatureStatuses"
	rpcMethodSimulateTransaction     = "simulateTransaction"
	rpcMethodRequestAirdrop          = "requestAirdrop"
	rpcMethodGetTransaction          = "getTransaction"
	rpcMethodGetSignaturesForAddress = "getSignaturesForAddress"
	rpcMethodGetBlocks               = "getBlocks"
	rpcMethodGetBlock                = "getBlock"
	rpcMethodGetBlockHeight          = "getBlockHeight"
)

type RPCMethodLimitsConfig struct {
	GlobalRPSLimit          int
	GetBalance              int
	GetTokenAccountBalance  int
	GetAccountInfo          int
	GetProgramAccounts      int
	GetLatestBlockhash      int
	GetSlot                 int
	SendTransaction         int
	GetSignatureStatuses    int
	SimulateTransaction     int
	RequestAirdrop          int
	GetTransaction          int
	GetSignaturesForAddress int
	GetBlocks               int
	GetBlock                int
	GetBlockHeight          int
}

type MutexRPCClient struct {
	rpcClient   *rpc.Client
	config      *RPCMethodLimitsConfig
	mu          sync.Mutex
	methodComps map[string]*methodComponents
}

type methodComponents struct {
	limiter *rate.Limiter
	mu      sync.Mutex
	// cooldownUntil gates calls to this method after a 429 response.
	// Guarded by mu. Only affects this method, never the whole client.
	cooldownUntil time.Time
}

func NewMutexRPCClient(rpcClient *rpc.Client, config *RPCMethodLimitsConfig) *MutexRPCClient {
	if config == nil {
		config = &RPCMethodLimitsConfig{
			GlobalRPSLimit:          7,
			GetBalance:              38,
			GetTokenAccountBalance:  40,
			GetAccountInfo:          40,
			GetProgramAccounts:      10,
			GetLatestBlockhash:      40,
			GetSlot:                 40,
			SendTransaction:         20,
			GetSignatureStatuses:    10,
			SimulateTransaction:     40,
			RequestAirdrop:          40,
			GetTransaction:          10,
			GetSignaturesForAddress: 10,
			GetBlocks:               40,
			GetBlock:                6,
			GetBlockHeight:          40,
		}
	}

	methodComponents := map[string]*methodComponents{
		rpcMethodGetBalance:             {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetBalance)},
		rpcMethodGetTokenAccountBalance: {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetTokenAccountBalance)},
		rpcMethodGetAccountInfo:         {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetAccountInfo)},
		rpcMethodGetProgramAccounts:     {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetProgramAccounts)},
		rpcMethodGetLatestBlockhash:     {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetLatestBlockhash)},
		rpcMethodGetSlot:                {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetSlot)},
		rpcMethodSendTransaction:        {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.SendTransaction)},
		rpcMethodGetSignatureStatuses: {
			limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetSignatureStatuses),
		},
		rpcMethodSimulateTransaction: {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.SimulateTransaction)},
		rpcMethodRequestAirdrop:      {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.RequestAirdrop)},
		rpcMethodGetTransaction:      {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetTransaction)},
		rpcMethodGetSignaturesForAddress: {
			limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetSignaturesForAddress),
		},
		rpcMethodGetBlocks:      {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetBlocks)},
		rpcMethodGetBlock:       {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetBlock)},
		rpcMethodGetBlockHeight: {limiter: newRPCMethodLimiter(rpcMethodLimitWindow, config.GetBlockHeight)},
	}

	return &MutexRPCClient{
		rpcClient:   rpcClient,
		methodComps: methodComponents,
	}
}

func newRPCMethodLimiter(rpcMethodLimitWindow time.Duration, rpcMethodLimitCount int) *rate.Limiter {
	// Reserve ~1s worth of the quota as burst capacity, and refill the rest
	// over the window, so burst + refill never exceeds rpcMethodLimitCount.
	burst := max(rpcMethodLimitCount/10, 1)
	sustained := rpcMethodLimitCount - burst

	return rate.NewLimiter(
		rate.Every(rpcMethodLimitWindow/time.Duration(sustained)),
		burst,
	)
}

const defaultRetryAfter = 10 * time.Second

// parseRetryAfter interprets the Retry-After response header (seconds or HTTP-date).
// Returns defaultRetryAfter when the header is missing or unparseable.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return defaultRetryAfter
	}

	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds <= 0 {
			return defaultRetryAfter
		}

		return time.Duration(seconds) * time.Second
	}

	if t, err := http.ParseTime(header); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}

		return defaultRetryAfter
	}

	return defaultRetryAfter
}

type rateLimitError struct {
	retryAfter time.Duration
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("rate limited (429), retry after %v", e.retryAfter)
}

// rateLimitRetryAfter reports whether err is a 429 rate-limit error and, if so,
// how long to back off. It recognises three shapes:
//   - *rateLimitError: produced by our getSignatureStatuses callback, carries the
//     precise Retry-After header.
//   - *jsonrpc.RPCError with Code 429: what solana-go returns when the node sends
//     a 429 with a well-formed JSON-RPC error body (the common devnet case).
//   - *jsonrpc.HTTPError with Code 429: what solana-go returns when the 429 body
//     could not be parsed into an RPC response.
//
// Only *rateLimitError carries the real Retry-After; the other two only expose
// the status code, so they fall back to defaultRetryAfter.
func rateLimitRetryAfter(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}

	var rl *rateLimitError
	if errors.As(err, &rl) {
		return rl.retryAfter, true
	}

	var rpcErr *jsonrpc.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == http.StatusTooManyRequests {
		return defaultRetryAfter, true
	}

	var httpErr *jsonrpc.HTTPError
	if errors.As(err, &httpErr) && httpErr.Code == http.StatusTooManyRequests {
		return defaultRetryAfter, true
	}

	return 0, false
}

func withRPCLimits[T any](
	c *MutexRPCClient,
	ctx context.Context,
	method string,
	call func() (T, error),
) (T, error) {
	var zero T

	methodComp := c.methodComps[method]
	if methodComp == nil {
		return zero, fmt.Errorf("method %s not found in methodComps", method)
	}

	// lock per method
	methodComp.mu.Lock()
	defer methodComp.mu.Unlock()

	for {
		// Honor any pending 429 cooldown for THIS method only. Holding
		// methodComp.mu here serializes same-method callers behind the backoff
		// without ever blocking other methods or holding the global c.mu.
		if wait := time.Until(methodComp.cooldownUntil); wait > 0 {
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(wait):
			}
		}

		if err := methodComp.limiter.Wait(ctx); err != nil {
			return zero, err
		}

		// lock per rpc call
		c.mu.Lock()
		result, err := call()
		c.mu.Unlock()

		retryAfter, rateLimited := rateLimitRetryAfter(err)
		if !rateLimited {
			return result, err
		}

		methodComp.cooldownUntil = time.Now().UTC().Add(retryAfter)
	}
}

func (c *MutexRPCClient) GetBalance(
	ctx context.Context,
	publicKey solana.PublicKey,
	commitment rpc.CommitmentType,
) (*rpc.GetBalanceResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetBalance, func() (*rpc.GetBalanceResult, error) {
		return c.rpcClient.GetBalance(ctx, publicKey, commitment)
	})
}

func (c *MutexRPCClient) GetTokenAccountBalance(
	ctx context.Context,
	account solana.PublicKey,
	commitment rpc.CommitmentType,
) (*rpc.GetTokenAccountBalanceResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetTokenAccountBalance, func() (*rpc.GetTokenAccountBalanceResult, error) {
		return c.rpcClient.GetTokenAccountBalance(ctx, account, commitment)
	})
}

func (c *MutexRPCClient) GetAccountInfoWithOpts(
	ctx context.Context,
	account solana.PublicKey,
	opts *rpc.GetAccountInfoOpts,
) (*rpc.GetAccountInfoResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetAccountInfo, func() (*rpc.GetAccountInfoResult, error) {
		return c.rpcClient.GetAccountInfoWithOpts(ctx, account, opts)
	})
}

func (c *MutexRPCClient) GetProgramAccountsWithOpts(
	ctx context.Context,
	publicKey solana.PublicKey,
	opts *rpc.GetProgramAccountsOpts,
) (rpc.GetProgramAccountsResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetProgramAccounts, func() (rpc.GetProgramAccountsResult, error) {
		return c.rpcClient.GetProgramAccountsWithOpts(ctx, publicKey, opts)
	})
}

func (c *MutexRPCClient) GetLatestBlockhash(
	ctx context.Context,
	commitment rpc.CommitmentType,
) (*rpc.GetLatestBlockhashResult, error) {
	return withRPCLimits(c, ctx, rpcMethodGetLatestBlockhash, func() (*rpc.GetLatestBlockhashResult, error) {
		return c.rpcClient.GetLatestBlockhash(ctx, commitment)
	})
}

func (c *MutexRPCClient) GetSlot(
	ctx context.Context,
	commitment rpc.CommitmentType,
) (uint64, error) {
	return withRPCLimits(c, ctx, rpcMethodGetSlot, func() (uint64, error) {
		return c.rpcClient.GetSlot(ctx, commitment)
	})
}

func (c *MutexRPCClient) SendTransactionWithOpts(
	ctx context.Context,
	transaction *solana.Transaction,
	opts rpc.TransactionOpts,
) (solana.Signature, error) {
	return withRPCLimits(c, ctx, rpcMethodSendTransaction, func() (solana.Signature, error) {
		return c.rpcClient.SendTransactionWithOpts(ctx, transaction, opts)
	})
}

// GetSignatureStatuses uses a callback so it can read the precise Retry-After
// header on a 429. The retry/backoff itself is handled centrally by
// withRPCLimits, which honors the returned *rateLimitError.
func (c *MutexRPCClient) GetSignatureStatuses(
	ctx context.Context,
	searchTransactionHistory bool,
	transactionSignatures ...solana.Signature,
) (*rpc.GetSignatureStatusesResult, error) {
	params := []interface{}{transactionSignatures}
	if searchTransactionHistory {
		params = append(params, rpc.M{"searchTransactionHistory": true})
	}

	return withRPCLimits(c, ctx, rpcMethodGetSignatureStatuses, func() (*rpc.GetSignatureStatusesResult, error) {
		var out *rpc.GetSignatureStatusesResult

		err := c.rpcClient.RPCCallWithCallback(ctx, "getSignatureStatuses", params,
			func(_ *http.Request, resp *http.Response) error {
				if resp.StatusCode == http.StatusTooManyRequests {
					retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))

					return &rateLimitError{retryAfter: retryAfter}
				}

				var rpcResponse jsonrpc.RPCResponse

				decoder := json.NewDecoder(resp.Body)
				decoder.UseNumber()

				if err := decoder.Decode(&rpcResponse); err != nil {
					if resp.StatusCode >= 400 {
						return fmt.Errorf("rpc call getSignatureStatuses status code: %v: %w", resp.StatusCode, err)
					}

					return fmt.Errorf("rpc call getSignatureStatuses: %w", err)
				}

				if rpcResponse.Error != nil {
					return rpcResponse.Error
				}

				return rpcResponse.GetObject(&out)
			},
		)
		if err != nil {
			return nil, err
		}

		if out == nil || out.Value == nil {
			return nil, rpc.ErrNotFound
		}

		return out, nil
	})
}

func (c *MutexRPCClient) SimulateTransactionWithOpts(
	ctx context.Context,
	transaction *solana.Transaction,
	opts *rpc.SimulateTransactionOpts,
) (*rpc.SimulateTransactionResponse, error) {
	return withRPCLimits(c, ctx, rpcMethodSimulateTransaction, func() (*rpc.SimulateTransactionResponse, error) {
		return c.rpcClient.SimulateTransactionWithOpts(ctx, transaction, opts)
	})
}

func (c *MutexRPCClient) RequestAirdrop(
	ctx context.Context,
	account solana.PublicKey,
	lamports uint64,
	commitment rpc.CommitmentType,
) (solana.Signature, error) {
	return withRPCLimits(c, ctx, rpcMethodRequestAirdrop, func() (solana.Signature, error) {
		return c.rpcClient.RequestAirdrop(ctx, account, lamports, commitment)
	})
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
