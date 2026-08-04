package trackertest

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/tracker"
	"github.com/Ethernal-Tech/solana-infrastructure/tracker/store"
	"github.com/stretchr/testify/require"
)

// CollectingSubscriber records every event the tracker dispatches, in dispatch order.
type CollectingSubscriber struct {
	mu        sync.Mutex
	collected []tracker.EventNotification
}

var _ tracker.EventSubscriber = (*CollectingSubscriber)(nil)

// AddEvent implements tracker.EventSubscriber.
func (s *CollectingSubscriber) AddEvent(event tracker.EventNotification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.collected = append(s.collected, event)

	return nil
}

// Events returns a copy of the events dispatched so far.
func (s *CollectingSubscriber) Events() []tracker.EventNotification {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]tracker.EventNotification(nil), s.collected...)
}

// NewBoltStorage returns a real bolt-backed storage handler on a temporary database, so the
// tracker's persistence and queue semantics are exercised rather than stubbed. The database
// is removed when the test finishes.
func NewBoltStorage(t *testing.T) *store.BoltStorageHandler {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tracker.db")

	storage, err := store.NewBoltStorageHandler(path, false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, storage.Close())
		require.NoError(t, os.Remove(path))
	})

	return storage
}

// RPCMethodLimits keeps the per-method rate limiters in play (the tracker's polling loops
// are otherwise unthrottled and would spin) while allowing far more throughput than the
// simulated chain produces. The limit window is 10s, so a count of N caps a method at N/10
// requests per second.
func RPCMethodLimits() *common.RPCMethodLimitsConfig {
	const perMethod = 300

	return &common.RPCMethodLimitsConfig{
		GlobalRPSLimit:          perMethod / 10,
		GetBalance:              perMethod,
		GetTokenAccountBalance:  perMethod,
		GetAccountInfo:          perMethod,
		GetProgramAccounts:      perMethod,
		GetLatestBlockhash:      perMethod,
		GetSlot:                 perMethod,
		SendTransaction:         perMethod,
		GetSignatureStatuses:    perMethod,
		SimulateTransaction:     perMethod,
		RequestAirdrop:          perMethod,
		GetTransaction:          perMethod,
		GetSignaturesForAddress: perMethod,
		GetBlocks:               perMethod,
		GetBlock:                perMethod,
		GetBlockHeight:          perMethod,
	}
}

// ProductionRPCMethodLimits mirrors the limits common.NewMutexRPCClient falls back to when a
// deployment passes no RPCMethodLimitsConfig, so a test can measure the tracker at the pace a
// real node actually answers it rather than at test speed. The limit window is 10s, so
// GetBlocks: 40 means four calls a second.
func ProductionRPCMethodLimits() *common.RPCMethodLimitsConfig {
	return &common.RPCMethodLimitsConfig{
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
		GetBlock:                20,
		GetBlockHeight:          40,
	}
}

// SyncBuffer is a concurrency-safe log sink, since the tracker logs from two goroutines.
type SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
