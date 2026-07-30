package tracker

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/test-go/testify/mock"

	"github.com/Ethernal-Tech/solana-infrastructure/tracker/store"
)

func TestGetSlotsToQueryBlocks(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		input     []uint64
		threshold uint64
		expected  []uint64
	}{
		{
			name:      "includes exact threshold slots",
			input:     []uint64{100, 101, 102, 200, 201, 300},
			threshold: 100,
			expected:  []uint64{100, 200, 300},
		},
		{
			name:      "includes last block before next threshold when boundary slot missing",
			input:     []uint64{95, 99, 101, 150, 199, 205},
			threshold: 100,
			expected:  []uint64{99, 199},
		},
		{
			name:      "does not duplicate when next slot is exact threshold",
			input:     []uint64{98, 99, 100, 101},
			threshold: 100,
			expected:  []uint64{100},
		},
		{
			name:      "empty input",
			input:     []uint64{},
			threshold: 100,
			expected:  []uint64{},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := getSlotsToQueryBlocks(tc.input, tc.threshold)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestChainHeadCatchUpWait(t *testing.T) {
	t.Parallel()

	t.Run("enough blocks needs no wait", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, time.Duration(0), chainHeadCatchUpWait(13))
		require.Equal(t, time.Duration(0), chainHeadCatchUpWait(15))
	})

	t.Run("partial batch waits for remaining slots", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 2400*time.Millisecond, chainHeadCatchUpWait(7))
	})

	t.Run("empty result waits for full target", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 5200*time.Millisecond, chainHeadCatchUpWait(0))
	})
}

func TestIsCursorNotFoundErr(t *testing.T) {
	t.Parallel()

	t.Run("tx not found rpc error", func(t *testing.T) {
		t.Parallel()

		err := &jsonrpc.RPCError{
			Code:    rpcErrCodeTxNotFound,
			Message: "Transaction 4P4F9XRqvKhQDCy44R1bg4vLMqUcpuBsLugomtY4MEbMS6Wtr not found",
		}
		require.True(t, IsCursorNotFoundErr(err))
		require.True(t, IsCursorNotFoundErr(fmt.Errorf("wrapped: %w", err)))
	})

	t.Run("other errors", func(t *testing.T) {
		t.Parallel()

		require.False(t, IsCursorNotFoundErr(nil))
		require.False(t, IsCursorNotFoundErr(errors.New("connection reset")))
		require.False(t, IsCursorNotFoundErr(&jsonrpc.RPCError{Code: -32005, Message: "Node is unhealthy"}))
	})
}

func TestTxSlotFloor(t *testing.T) {
	t.Parallel()

	t.Run("no watermark falls back to start from slot", func(t *testing.T) {
		t.Parallel()

		tracker := &EventTracker{startFromSlot: 100}
		require.Equal(t, uint64(100), tracker.txSlotFloor())
	})

	t.Run("watermark sits one slot above the last queried slot", func(t *testing.T) {
		t.Parallel()

		tracker := &EventTracker{startFromSlot: 100, lastQueriedTxSlot: 500}
		require.Equal(t, uint64(501), tracker.txSlotFloor())
	})

	t.Run("start from slot wins when it is ahead of the watermark", func(t *testing.T) {
		t.Parallel()

		tracker := &EventTracker{startFromSlot: 1000, lastQueriedTxSlot: 500}
		require.Equal(t, uint64(1000), tracker.txSlotFloor())
	})
}

func TestAdvanceLastQueriedTxSlot(t *testing.T) {
	t.Parallel()

	newTracker := func(t *testing.T, commitment rpc.CommitmentType, lastQueriedTxSlot uint64,
	) (*EventTracker, *store.MockStorageHandler) {
		t.Helper()

		storage := &store.MockStorageHandler{}

		return &EventTracker{
			storage:           storage,
			commitment:        commitment,
			logger:            hclog.NewNullLogger(),
			lastQueriedTxSlot: lastQueriedTxSlot,
		}, storage
	}

	// signatures are returned newest first
	signatures := []*rpc.TransactionSignature{
		{Slot: 300, ConfirmationStatus: rpc.ConfirmationStatusConfirmed},
		{Slot: 299, ConfirmationStatus: rpc.ConfirmationStatusConfirmed},
		{Slot: 250, ConfirmationStatus: rpc.ConfirmationStatusFinalized},
		{Slot: 240, ConfirmationStatus: rpc.ConfirmationStatusFinalized},
	}

	t.Run("advances to newest finalized signature only", func(t *testing.T) {
		t.Parallel()

		tracker, storage := newTracker(t, rpc.CommitmentConfirmed, 100)
		storage.On("SetLastQueriedTxSlot", uint64(250)).Return(nil).Once()

		require.NoError(t, tracker.advanceLastQueriedTxSlot(signatures))
		require.Equal(t, uint64(250), tracker.lastQueriedTxSlot)
		storage.AssertExpectations(t)
	})

	t.Run("never moves backwards", func(t *testing.T) {
		t.Parallel()

		tracker, storage := newTracker(t, rpc.CommitmentConfirmed, 260)

		require.NoError(t, tracker.advanceLastQueriedTxSlot(signatures))
		require.Equal(t, uint64(260), tracker.lastQueriedTxSlot)
		storage.AssertNotCalled(t, "SetLastQueriedTxSlot", mock.Anything)
	})

	t.Run("no finalized signature leaves the watermark untouched", func(t *testing.T) {
		t.Parallel()

		tracker, storage := newTracker(t, rpc.CommitmentConfirmed, 100)

		require.NoError(t, tracker.advanceLastQueriedTxSlot(signatures[:2]))
		require.Equal(t, uint64(100), tracker.lastQueriedTxSlot)
		storage.AssertNotCalled(t, "SetLastQueriedTxSlot", mock.Anything)
	})

	t.Run("at finalized commitment every signature counts", func(t *testing.T) {
		t.Parallel()

		tracker, storage := newTracker(t, rpc.CommitmentFinalized, 100)
		storage.On("SetLastQueriedTxSlot", uint64(300)).Return(nil).Once()

		// confirmationStatus is not populated by every node
		require.NoError(t, tracker.advanceLastQueriedTxSlot([]*rpc.TransactionSignature{{Slot: 300}}))
		require.Equal(t, uint64(300), tracker.lastQueriedTxSlot)
		storage.AssertExpectations(t)
	})

	t.Run("storage failure keeps the in-memory watermark", func(t *testing.T) {
		t.Parallel()

		tracker, storage := newTracker(t, rpc.CommitmentConfirmed, 100)
		storage.On("SetLastQueriedTxSlot", uint64(250)).Return(errors.New("write failed")).Once()

		require.ErrorContains(t, tracker.advanceLastQueriedTxSlot(signatures), "write failed")
		require.Equal(t, uint64(100), tracker.lastQueriedTxSlot)
		storage.AssertExpectations(t)
	})
}

func TestBootstrapLastQueriedTxSlot(t *testing.T) {
	t.Parallel()

	t.Run("seeds the watermark from the newest stored event", func(t *testing.T) {
		t.Parallel()

		storage := &store.MockStorageHandler{}
		storage.On("GetLatestEventSlot").Return(uint64(479814919), nil).Once()
		storage.On("SetLastQueriedTxSlot", uint64(479814919)).Return(nil).Once()

		tracker := &EventTracker{storage: storage, logger: hclog.NewNullLogger()}

		require.NoError(t, tracker.bootstrapLastQueriedTxSlot())
		require.Equal(t, uint64(479814919), tracker.lastQueriedTxSlot)
		require.Equal(t, uint64(479814920), tracker.txSlotFloor())
		storage.AssertExpectations(t)
	})

	t.Run("keeps an existing watermark", func(t *testing.T) {
		t.Parallel()

		storage := &store.MockStorageHandler{}
		tracker := &EventTracker{
			storage:           storage,
			logger:            hclog.NewNullLogger(),
			lastQueriedTxSlot: 500,
		}

		require.NoError(t, tracker.bootstrapLastQueriedTxSlot())
		require.Equal(t, uint64(500), tracker.lastQueriedTxSlot)
		storage.AssertNotCalled(t, "GetLatestEventSlot")
	})

	t.Run("stays unset when no events are stored", func(t *testing.T) {
		t.Parallel()

		storage := &store.MockStorageHandler{}
		storage.On("GetLatestEventSlot").Return(uint64(0), nil).Once()

		tracker := &EventTracker{storage: storage, logger: hclog.NewNullLogger()}

		require.NoError(t, tracker.bootstrapLastQueriedTxSlot())
		require.Equal(t, uint64(0), tracker.lastQueriedTxSlot)
		storage.AssertNotCalled(t, "SetLastQueriedTxSlot", mock.Anything)
	})
}
