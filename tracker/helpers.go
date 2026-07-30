package tracker

import (
	"context"
	"errors"
	"time"

	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"github.com/hashicorp/go-hclog"
)

const (
	defaultRetryCount    = 10
	defaultRetryWaitTime = time.Second * 5

	// rpcErrCodeTxNotFound is returned by Solana as
	// `Transaction <signature> not found` when a signature passed as a
	// before/until cursor to getSignaturesForAddress cannot be resolved by the
	// node. It means the cursor is permanently unusable against that node, not
	// that the call should be retried.
	rpcErrCodeTxNotFound = -32020
)

var (
	ErrRetryTimeout  = errors.New("timeout")
	ErrRetryTryAgain = errors.New("retry try again")
	defaultLogger    = hclog.NewNullLogger()
)

// RetryConfig defines ExecuteWithRetry configuration
type RetryConfig struct {
	retryCount       int
	retryWaitTime    time.Duration
	isRetryableError func(err error) bool
	logger           hclog.Logger
}

// RetryConfigOption defines ExecuteWithRetry configuration option
type RetryConfigOption func(c *RetryConfig)

func IsRetryableError(err error) bool {
	return errors.Is(err, ErrRetryTryAgain)
}

// IsCursorNotFoundErr reports whether err is the Solana `Transaction <signature>
// not found` error returned when a signature used as a before/until cursor
// cannot be resolved by the node. That happens when the signature aged out of
// the node's transaction history, the ledger was reset (or a different endpoint
// is being used), or the transaction was dropped on a fork before it finalized.
// Such an error is permanent for that cursor, so retrying the same call is
// pointless - the cursor has to be replaced.
func IsCursorNotFoundErr(err error) bool {
	var rpcErr *jsonrpc.RPCError

	return errors.As(err, &rpcErr) && rpcErr.Code == rpcErrCodeTxNotFound
}

func WithRetryCount(retryCount int) RetryConfigOption {
	return func(c *RetryConfig) {
		c.retryCount = retryCount
	}
}

func WithRetryWaitTime(retryWaitTime time.Duration) RetryConfigOption {
	return func(c *RetryConfig) {
		c.retryWaitTime = retryWaitTime
	}
}

// ExecuteWithRetry attempts to execute a provided handler function multiple times
// with retries in case of failure, respecting a specified wait time between attempts.
func ExecuteWithRetry[T any](
	ctx context.Context, handler func(context.Context) (T, error), options ...RetryConfigOption,
) (result T, err error) {
	config := RetryConfig{
		retryCount:       defaultRetryCount,
		retryWaitTime:    defaultRetryWaitTime,
		isRetryableError: IsRetryableError,
		logger:           defaultLogger,
	}

	for _, opt := range options {
		opt(&config)
	}

	for count := 0; count < config.retryCount; count++ {
		result, err = handler(ctx)
		if err != nil {
			if !config.isRetryableError(err) {
				return result, err
			}

			if !errors.Is(err, ErrRetryTryAgain) { // do not log ErrRetryTryAgain errors
				config.logger.Info("ExecuteWithRetry failed. Retrying...", "time", count+1, "err", err)
			}
		} else {
			return result, nil
		}

		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(config.retryWaitTime):
		}
	}

	return result, ErrRetryTimeout
}
