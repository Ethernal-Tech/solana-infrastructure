package tracker

import (
	"fmt"
	"testing"

	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"github.com/test-go/testify/assert"
)

func TestIsCleanedUpBlockError_JSONRPCError(t *testing.T) {
	// gagliardetto's RPCError.Error() spew-dumps the struct; detection must use Message, not err.Error().
	err := &jsonrpc.RPCError{
		Code:    -32001,
		Message: "Block 516 cleaned up, does not exist on node. First available block: 517",
	}
	ok, firstAvail := isCleanedUpBlockError(err, 516)
	assert.True(t, ok)
	assert.Equal(t, uint64(517), firstAvail)

	ok, _ = isCleanedUpBlockError(err, 517)
	assert.False(t, ok, "wrong slot should not match")
}

func TestIsCleanedUpBlockError_WrappedRPCError(t *testing.T) {
	inner := &jsonrpc.RPCError{
		Code:    -32001,
		Message: "Block 516 cleaned up, does not exist on node. First available block: 517",
	}
	ok, firstAvail := isCleanedUpBlockError(fmt.Errorf("get block: %w", inner), 516)
	assert.True(t, ok)
	assert.Equal(t, uint64(517), firstAvail)
}

func TestIsBlockNotAvailableForSlotError_JSONRPCError(t *testing.T) {
	err := &jsonrpc.RPCError{
		Code:    -32004,
		Message: "Block not available for slot 514",
	}
	assert.True(t, isBlockNotAvailableForSlotError(err, 514))
	assert.False(t, isBlockNotAvailableForSlotError(err, 515), "slot in message must match")
}

func TestIsBlockNotAvailableForSlotError_WrappedRPCError(t *testing.T) {
	inner := &jsonrpc.RPCError{
		Code:    -32004,
		Message: "Block not available for slot 514",
	}
	assert.True(t, isBlockNotAvailableForSlotError(fmt.Errorf("get block: %w", inner), 514))
}

func TestIsBlockNotAvailableForSlotError_WrongRPCCode(t *testing.T) {
	// Same wording but different code must not count as block-not-available.
	err := &jsonrpc.RPCError{
		Code:    -32007,
		Message: "Block not available for slot 514",
	}
	assert.False(t, isBlockNotAvailableForSlotError(err, 514))
}
