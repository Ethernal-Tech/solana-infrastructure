package store

import (
	"os"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
)

func TestBoltStorageHandler_GetEventsBySlot(t *testing.T) {
	t.Parallel()

	dbPath, err := os.CreateTemp("", "solana-store-test-*")
	require.NoError(t, err)

	require.NoError(t, dbPath.Close())
	require.NoError(t, os.Remove(dbPath.Name()))

	handler, err := NewBoltStorageHandler(dbPath.Name(), false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, handler.Close())
		require.NoError(t, os.Remove(dbPath.Name()))
	})

	programID := solana.NewWallet().PublicKey()
	sig1, err := solana.NewWallet().PrivateKey.Sign([]byte("tx1"))
	require.NoError(t, err)
	sig2, err := solana.NewWallet().PrivateKey.Sign([]byte("tx2"))
	require.NoError(t, err)
	sig3, err := solana.NewWallet().PrivateKey.Sign([]byte("tx3"))
	require.NoError(t, err)

	type testEvent struct {
		Value int `json:"value"`
	}

	require.NoError(t, handler.StoreEvent(
		nil, 10, sig1, programID, "BridgeRequestEvent", [32]byte{}, &testEvent{Value: 1}))
	require.NoError(t, handler.StoreEvent(
		nil, 10, sig2, programID, "TransactionExecutedEvent", [32]byte{}, &testEvent{Value: 2}))
	require.NoError(t, handler.StoreEvent(
		nil, 11, sig3, programID, "BridgeRequestEvent", [32]byte{}, &testEvent{Value: 3}))

	events, err := handler.GetEventsBySlot(10)
	require.NoError(t, err)
	require.Len(t, events, 2)

	events, err = handler.GetEventsBySlot(11)
	require.NoError(t, err)
	require.Len(t, events, 1)

	events, err = handler.GetEventsBySlot(99)
	require.NoError(t, err)
	require.Empty(t, events)

	eventsBeforeMark, err := handler.GetUnprocessedEvents(10)
	require.NoError(t, err)
	require.Len(t, eventsBeforeMark, 3)

	require.NoError(t, handler.MarkEventAsProcessed(eventsBeforeMark[0].ID))

	events, err = handler.GetEventsBySlot(10)
	require.NoError(t, err)
	require.Len(t, events, 2)
}
