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
}

func TestBoltStorageHandler_UnprocessedTransactions(t *testing.T) {
	t.Parallel()

	dbPath, err := os.CreateTemp("", "solana-store-tx-queue-test-*")
	require.NoError(t, err)

	require.NoError(t, dbPath.Close())
	require.NoError(t, os.Remove(dbPath.Name()))

	handler, err := NewBoltStorageHandler(dbPath.Name(), false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, handler.Close())
		require.NoError(t, os.Remove(dbPath.Name()))
	})

	sigA, err := solana.NewWallet().PrivateKey.Sign([]byte("a"))
	require.NoError(t, err)
	sigB, err := solana.NewWallet().PrivateKey.Sign([]byte("b"))
	require.NoError(t, err)
	sigC, err := solana.NewWallet().PrivateKey.Sign([]byte("c"))
	require.NoError(t, err)

	queue, err := handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Empty(t, queue)

	require.NoError(t, handler.PushUnprocessedTransactions([]solana.Signature{sigA, sigB}))
	require.NoError(t, handler.PushUnprocessedTransactions([]solana.Signature{sigC}))

	queue, err = handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Equal(t, []solana.Signature{sigA, sigB, sigC}, queue)

	require.NoError(t, handler.RemoveProcessedTransaction(sigA))

	queue, err = handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Equal(t, []solana.Signature{sigB, sigC}, queue)

	err = handler.RemoveProcessedTransaction(sigC)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not at the front")

	require.NoError(t, handler.RemoveProcessedTransaction(sigB))
	require.NoError(t, handler.RemoveProcessedTransaction(sigC))

	queue, err = handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Empty(t, queue)
}

func TestBoltStorageHandler_LastProcessedTransaction(t *testing.T) {
	t.Parallel()

	dbPath, err := os.CreateTemp("", "solana-store-last-processed-test-*")
	require.NoError(t, err)

	require.NoError(t, dbPath.Close())
	require.NoError(t, os.Remove(dbPath.Name()))

	handler, err := NewBoltStorageHandler(dbPath.Name(), false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, handler.Close())
		require.NoError(t, os.Remove(dbPath.Name()))
	})

	lastProcessed, err := handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, solana.Signature{}, lastProcessed)

	sig, err := solana.NewWallet().PrivateKey.Sign([]byte("processed"))
	require.NoError(t, err)

	require.NoError(t, handler.SetLastProcessedTransaction(sig))

	lastProcessed, err = handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, sig, lastProcessed)

	sig2, err := solana.NewWallet().PrivateKey.Sign([]byte("processed-2"))
	require.NoError(t, err)

	require.NoError(t, handler.SetLastProcessedTransaction(sig2))

	lastProcessed, err = handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, sig2, lastProcessed)
}
