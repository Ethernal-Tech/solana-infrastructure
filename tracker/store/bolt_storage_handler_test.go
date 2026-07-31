package store

import (
	"os"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
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
		nil, 10, 100, sig1, programID, "BridgeRequestEvent", [32]byte{}, &testEvent{Value: 1}))
	require.NoError(t, handler.StoreEvent(
		nil, 10, 100, sig2, programID, "TransactionExecutedEvent", [32]byte{}, &testEvent{Value: 2}))
	require.NoError(t, handler.StoreEvent(
		nil, 11, 101, sig3, programID, "BridgeRequestEvent", [32]byte{}, &testEvent{Value: 3}))

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

func TestBoltStorageHandler_GetProcessedTxSignaturesBySlot(t *testing.T) {
	t.Parallel()

	dbPath, err := os.CreateTemp("", "solana-store-processed-sigs-test-*")
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

	// sig1 emits two events in the same slot, so it must be reported once
	require.NoError(t, handler.StoreEvent(
		nil, 10, 100, sig1, programID, "BridgeRequestEvent", [32]byte{}, &testEvent{Value: 1}))
	require.NoError(t, handler.StoreEvent(
		nil, 10, 100, sig1, programID, "TransactionExecutedEvent", [32]byte{}, &testEvent{Value: 2}))
	require.NoError(t, handler.StoreEvent(
		nil, 10, 100, sig2, programID, "BridgeRequestEvent", [32]byte{}, &testEvent{Value: 3}))
	require.NoError(t, handler.StoreEvent(
		nil, 11, 101, sig3, programID, "BridgeRequestEvent", [32]byte{}, &testEvent{Value: 4}))

	// distinct, in the order the events were stored
	signatures, err := handler.GetProcessedTxSignaturesBySlot(10)
	require.NoError(t, err)
	require.Equal(t, []solana.Signature{sig1, sig2}, signatures)

	signatures, err = handler.GetProcessedTxSignaturesBySlot(11)
	require.NoError(t, err)
	require.Equal(t, []solana.Signature{sig3}, signatures)

	signatures, err = handler.GetProcessedTxSignaturesBySlot(99)
	require.NoError(t, err)
	require.Empty(t, signatures)
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

	pointA := TxPoint{TxSignature: sigA, Slot: 10}
	pointB := TxPoint{TxSignature: sigB, Slot: 11}
	pointC := TxPoint{TxSignature: sigC, Slot: 12}

	queue, err := handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Empty(t, queue)

	require.NoError(t, handler.PushUnprocessedTransactions([]TxPoint{pointA, pointB}))
	require.NoError(t, handler.PushUnprocessedTransactions([]TxPoint{pointC}))

	queue, err = handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Equal(t, []TxPoint{pointA, pointB, pointC}, queue)

	emptyPoint := TxPoint{}

	require.NoError(t, handler.PushUnprocessedTransactions([]TxPoint{
		emptyPoint,
		emptyPoint,
	}))

	queue, err = handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Equal(t, []TxPoint{pointA, pointB, pointC}, queue)

	require.NoError(t, handler.RemoveProcessedTransaction(sigA))

	queue, err = handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Equal(t, []TxPoint{pointB, pointC}, queue)

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
	require.Equal(t, TxPoint{}, lastProcessed)

	sig, err := solana.NewWallet().PrivateKey.Sign([]byte("processed"))
	require.NoError(t, err)

	point := TxPoint{TxSignature: sig, Slot: 42}

	require.NoError(t, handler.SetLastProcessedTransaction(point))

	lastProcessed, err = handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, point, lastProcessed)

	sig2, err := solana.NewWallet().PrivateKey.Sign([]byte("processed-2"))
	require.NoError(t, err)

	point2 := TxPoint{TxSignature: sig2, Slot: 43}

	require.NoError(t, handler.SetLastProcessedTransaction(point2))

	lastProcessed, err = handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, point2, lastProcessed)
}

func TestBoltStorageHandler_LatestQueriedTransaction(t *testing.T) {
	t.Parallel()

	dbPath, err := os.CreateTemp("", "solana-store-last-queried-test-*")
	require.NoError(t, err)

	require.NoError(t, dbPath.Close())
	require.NoError(t, os.Remove(dbPath.Name()))

	handler, err := NewBoltStorageHandler(dbPath.Name(), false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, handler.Close())
		require.NoError(t, os.Remove(dbPath.Name()))
	})

	latestQueried, err := handler.GetLatestQueriedTransaction()
	require.NoError(t, err)
	require.Equal(t, TxPoint{}, latestQueried)

	sig, err := solana.NewWallet().PrivateKey.Sign([]byte("queried"))
	require.NoError(t, err)

	point := TxPoint{TxSignature: sig, Slot: 99}

	require.NoError(t, handler.StoreLatestQueriedTransaction(point))

	latestQueried, err = handler.GetLatestQueriedTransaction()
	require.NoError(t, err)
	require.Equal(t, point, latestQueried)

	// the queried and processed records are independent
	lastProcessed, err := handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, TxPoint{}, lastProcessed)

	sig2, err := solana.NewWallet().PrivateKey.Sign([]byte("queried-2"))
	require.NoError(t, err)

	point2 := TxPoint{TxSignature: sig2, Slot: 100}

	require.NoError(t, handler.StoreLatestQueriedTransaction(point2))

	latestQueried, err = handler.GetLatestQueriedTransaction()
	require.NoError(t, err)
	require.Equal(t, point2, latestQueried)
}

// Entries written before slots were persisted hold only the 64-byte signature and
// must still decode, with slot 0.
func TestBoltStorageHandler_PreSlotEntriesDecode(t *testing.T) {
	t.Parallel()

	dbPath, err := os.CreateTemp("", "solana-store-pre-slot-test-*")
	require.NoError(t, err)

	require.NoError(t, dbPath.Close())
	require.NoError(t, os.Remove(dbPath.Name()))

	handler, err := NewBoltStorageHandler(dbPath.Name(), false)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, handler.Close())
		require.NoError(t, os.Remove(dbPath.Name()))
	})

	sigA, err := solana.NewWallet().PrivateKey.Sign([]byte("queued"))
	require.NoError(t, err)
	sigB, err := solana.NewWallet().PrivateKey.Sign([]byte("processed"))
	require.NoError(t, err)

	// write raw signature-only values, as the previous encoding did
	require.NoError(t, handler.db.Update(func(tx *bolt.Tx) error {
		bucket, err := handler.unprocessedTxSignaturesBucket(tx)
		require.NoError(t, err)

		require.NoError(t, bucket.Put(encodeUint64(0), sigA[:]))
		require.NoError(t, storeTxQueueHead(bucket, 0))
		require.NoError(t, storeTxQueueTail(bucket, 1))

		return bucket.Put(lastProcessedTxSignatureKey, sigB[:])
	}))

	queue, err := handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Equal(t, []TxPoint{{TxSignature: sigA}}, queue)

	lastProcessed, err := handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, TxPoint{TxSignature: sigB}, lastProcessed)

	// finalizing a pre-slot entry keeps working
	require.NoError(t, handler.FinalizeProcessedTransaction(sigA))

	lastProcessed, err = handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, TxPoint{TxSignature: sigA}, lastProcessed)
}

func TestBoltStorageHandler_FinalizeProcessedTransaction(t *testing.T) {
	t.Parallel()

	dbPath, err := os.CreateTemp("", "solana-store-finalize-tx-test-*")
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

	pointA := TxPoint{TxSignature: sigA, Slot: 7}
	pointB := TxPoint{TxSignature: sigB, Slot: 8}

	require.NoError(t, handler.PushUnprocessedTransactions([]TxPoint{pointA, pointB}))

	require.NoError(t, handler.FinalizeProcessedTransaction(sigA))

	// finalizing carries over the slot recorded when the entry was pushed
	lastProcessed, err := handler.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, pointA, lastProcessed)

	queue, err := handler.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Equal(t, []TxPoint{pointB}, queue)

	err = handler.FinalizeProcessedTransaction(sigA)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not at the front")
}
