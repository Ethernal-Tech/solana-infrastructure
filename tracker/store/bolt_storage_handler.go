package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/gagliardetto/solana-go"
	bolt "go.etcd.io/bbolt"
)

// StorageTransaction represents a transaction object from the underlying storage backend. The
// actual (dynamic) type depends on the storage implementation being used (for example, *sql.Tx
// for relational DB or *bolt.Tx for Bolt DB ). See [ApplyTransaction] for more information on
// how transactions are managed during slot processing.
type StorageTransaction any

// StorageHandler defines the interface that must be implemented by any storage backend used
// with the [EventTracker]. The event tracker processes blocks and Anchor-based events from
// the Solana blockchain sequentially, and relies on this interface (that is, the underlying
// storage) to persist both the indexing progress (which slot should be processed next) and the
// actual event data that is extracted. Implementations of this interface can use any storage
// mechanism, such as a relational database, a key-value store, or a file-based system, as long
// as they correctly implement the methods described below.
type StorageHandler interface {
	// ReadSlot is invoked exactly once by the tracker during startup, that is, when the [Start]
	// method is called. This method must return the slot number from which the tracker should
	// begin monitoring and processing blocks (for example, if it returns 247, then slot 247
	// will be the first slot processed by the tracker upon startup). If the method returns an
	// error, the tracker will not even start and will terminate immediately.
	ReadSlot() (uint64, error)

	// StoreSlot is invoked by the tracker after every successfully processed slot. Note that a
	// slot does not necessarily contain a block, however, the method will still be invoked for
	// those empty slots. In transaction-like mode, the method is not invoked directly but rather
	// wrapped and passed to [ApplyTransaction]. The first argument is a transaction object from
	// the underlying storage backend, see [ApplyTransaction] for more information. The second
	// argument is the slot number that was just processed, and the implementation is responsible
	// for incrementing this value by one before storing it. This ensures that when [ReadSlot] is
	// invoked on the next startup, it returns correct starting slot. If the method returns an
	// error, the tracker will terminate immediately.
	StoreSlot(StorageTransaction, uint64) error

	// StoreBlock is invoked by the tracker after every successfully processed block. Note that a
	// block does not necessarily contain transactions, however, the method will still be invoked
	// for those empty blocks. In transaction-like mode, the method is not invoked directly but rather
	// wrapped and passed to [ApplyTransaction]. The first argument is a transaction object from the
	// underlying storage backend, see [ApplyTransaction] for more information. The second argument is
	// the slot number of the block, and the third argument is the block hash. The implementation is
	// responsible for storing the block hash for the given slot. If the method returns an error, the
	// tracker will terminate immediately.
	StoreBlock(StorageTransaction, uint64, solana.Hash) error

	// GetBlockhashBySlot returns the block hash stored for the given slot. If no hash exists for
	// that exact slot (i.e. it was an empty/skipped slot), it walks forward by incrementing the
	// slot number until a hash is found or the current indexing head (ReadSlot) is exceeded, in
	// which case an error is returned.
	GetBlockhashBySlot(uint64) (solana.Hash, error)

	// GetSlotByBlockhash returns the slot number stored for the given block hash.
	// If no slot exists for that hash, it walks backward by decrementing the slot number
	// until a slot is found or the genesis slot is reached, in which case an error is returned.
	GetSlotByBlockhash(solana.Hash) (uint64, error)

	// StoreLatestBlockPoint is invoked by the tracker after every successfully processed block.
	StoreLatestBlockPoint(StorageTransaction, BlockPoint) error

	// GetLatestBlockPoint returns the latest block point data, i.e. block slot and block hash.
	GetLatestBlockPoint() (*BlockPoint, error)

	// StoreEvent is invoked by the tracker after each successfully processed tracked event. In
	// transaction-like mode, the method is not invoked directly but rather wrapped and passed to
	// [ApplyTransaction]. The first argument is a transaction object from the underlying storage
	// backend, see [ApplyTransaction] for more information. The remaining arguments are, in order:
	// the slot number in which the event occurred, the signature of the transaction that generated
	// the event, the public key (address) of the Solana program that emitted the event, the event
	// name as registered in the [ProgramEventSpecs] config, and the deserialized event itself. If
	// the method returns an error, the tracker will terminate immediately.
	StoreEvent(StorageTransaction, uint64, solana.Signature, solana.PublicKey, string, [32]byte, any) error

	// UseTransactions is invoked exactly once by the tracker during startup, that is, when the
	// [Start] method is called. This method should return true if the storage backend supports
	// a transaction-like (all-or-nothing) mode and the tracker should use it. Using transactional
	// writes is the recommended approach because it prevents data inconsistency that can occur
	// during system restarts. Without it, if the tracker processes a slot with multiple events
	// and successfully stores some of them before crashing, the slot number will not have been
	// updated. When the tracker starts again, it will process the same slot again, leading to
	// duplicate event entries in storage. By using transactions, either all events from a slot
	// are stored together with the updated slot, or none of them are.
	UseTransactions() bool

	// ApplyTransaction is invoked by the tracker after processing each slot, but only if the
	// invocation of [UseTransactions] returned true. This method receives two arguments: first,
	// a function that wraps the invocation of the [StoreSlot] method for the current slot, and
	// second, a list of functions where each one wraps a [StoreEvent] invocation for a single
	// tracked event found in that slot. Each wrapper function accepts a transaction object as
	// its argument, which it then passes to the underlying [StoreSlot] or [StoreEvent] method
	// call. So, the implementation should create/begin a transaction, call all wrapper functions
	// by passing the transaction object to each one, and then commit or rollback the transaction
	// after all invocations complete. This approach ensures that either all storage operations
	// for a given slot are persisted together atomically, or none of them are saved if any
	// operation fails. If this method returns an error, the tracker will terminate immediately.
	ApplyTransaction(func(StorageTransaction) error, []func(StorageTransaction) error) error

	Close() error
}

type BoltStorageHandler struct {
	txMode bool
	db     *bolt.DB
}

var _ StorageHandler = &BoltStorageHandler{}

// EventRecord represents a stored event with metadata
type EventRecord struct {
	ID              uint64                 `json:"id"`
	Slot            uint64                 `json:"slot"`
	TxSignature     string                 `json:"tx_signature"`
	Program         string                 `json:"program"`
	EventType       string                 `json:"event_type"`
	InnerActionHash [32]byte               `json:"inner_action_hash"`
	Data            map[string]interface{} `json:"data"`
}

type BlockPoint struct {
	BlockSlot uint64      `json:"slot"`
	BlockHash solana.Hash `json:"hash"`
}

var (
	slotBucket              = []byte("slot")
	blocksBucket            = []byte("blocks")
	latestBlockPointBucket  = []byte("latestBlockPoint")
	unprocessedEventsBucket = []byte("unprocessed_events")
	processedEventsBucket   = []byte("processed_events")
	eventIDCounterBucket    = []byte("event_id_counter")

	currentSlotKey      = []byte("current")
	latestBlockPointKey = []byte("latestBlockPointKey")
)

func NewBoltStorageHandler(path string, txMode bool) (*BoltStorageHandler, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot open bolt db: %w", err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(slotBucket)
		if err != nil {
			return fmt.Errorf("cannot create the slot bucket: %w", err)
		}

		_, err = tx.CreateBucketIfNotExists(blocksBucket)
		if err != nil {
			return fmt.Errorf("cannot create the blocks bucket: %w", err)
		}

		_, err = tx.CreateBucketIfNotExists(latestBlockPointBucket)
		if err != nil {
			return fmt.Errorf("cannot create the latestBlockPointBucket bucket: %w", err)
		}

		// Create unprocessed events bucket
		_, err = tx.CreateBucketIfNotExists(unprocessedEventsBucket)
		if err != nil {
			return fmt.Errorf("cannot create the unprocessed events bucket: %w", err)
		}

		// Create processed events bucket
		_, err = tx.CreateBucketIfNotExists(processedEventsBucket)
		if err != nil {
			return fmt.Errorf("cannot create the processed events bucket: %w", err)
		}

		// Create event ID counter bucket
		_, err = tx.CreateBucketIfNotExists(eventIDCounterBucket)
		if err != nil {
			return fmt.Errorf("cannot create the event ID counter bucket: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return &BoltStorageHandler{txMode, db}, nil
}

func (b *BoltStorageHandler) Close() error {
	return b.db.Close()
}

func encodeUint64(v uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)

	return buf
}

func decodeUint64(buf []byte) uint64 {
	return binary.BigEndian.Uint64(buf)
}

// Retrieves and increments the event ID counter
func (b *BoltStorageHandler) getNextEventID(tx *bolt.Tx) (uint64, error) {
	bucket := tx.Bucket(eventIDCounterBucket)
	if bucket == nil {
		return 0, fmt.Errorf("event ID counter bucket not found")
	}

	counterKey := []byte("counter")
	value := bucket.Get(counterKey)

	var nextID uint64
	if value == nil {
		nextID = 1
	} else {
		nextID = decodeUint64(value) + 1
	}

	if err := bucket.Put(counterKey, encodeUint64(nextID)); err != nil {
		return 0, fmt.Errorf("failed to update event ID counter: %w", err)
	}

	return nextID, nil
}

func (b *BoltStorageHandler) ReadSlot() (uint64, error) {
	var retValue uint64

	if err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(slotBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find slot bucket")
		}

		value := bucket.Get(currentSlotKey)
		if value == nil {
			retValue = 0
		} else {
			retValue = decodeUint64(value)
		}

		return nil
	}); err != nil {
		return 0, err
	}

	return retValue, nil
}

func (b *BoltStorageHandler) StoreSlot(tx StorageTransaction, slot uint64) error {
	storeFn := func(tx *bolt.Tx) error {
		bucket := tx.Bucket(slotBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find slot bucket")
		}

		return bucket.Put(currentSlotKey, encodeUint64(slot+1))
	}

	if tx == nil {
		return b.db.Update(storeFn)
	}

	if tx, ok := tx.(*bolt.Tx); ok {
		return storeFn(tx)
	}

	return fmt.Errorf("unknown storage transaction type: %T", tx)
}

func (b *BoltStorageHandler) StoreBlock(tx StorageTransaction, slot uint64, hash solana.Hash) error {
	storeFn := func(tx *bolt.Tx) error {
		bucket := tx.Bucket(blocksBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find blocks bucket")
		}

		return bucket.Put(encodeUint64(slot), hash[:])
	}

	if tx == nil {
		return b.db.Update(storeFn)
	}

	if tx, ok := tx.(*bolt.Tx); ok {
		return storeFn(tx)
	}

	return fmt.Errorf("unknown storage transaction type: %T", tx)
}

func (b *BoltStorageHandler) StoreEvent(
	tx StorageTransaction,
	slot uint64,
	txSignature solana.Signature,
	programID solana.PublicKey,
	eventName string,
	innerActionHash [32]byte,
	eventData any) error {
	storeFn := func(tx *bolt.Tx) error {
		// Generate unique event ID
		eventID, err := b.getNextEventID(tx)
		if err != nil {
			return err
		}

		// Get unprocessed events bucket
		unprocessedBucket := tx.Bucket(unprocessedEventsBucket)
		if unprocessedBucket == nil {
			return fmt.Errorf("unprocessed events bucket not found")
		}

		// Marshal event data (extracting from pointer)
		eventDataValue, err := json.Marshal(reflect.ValueOf(eventData).Elem().Interface())
		if err != nil {
			return fmt.Errorf("cannot serialize event data: %w", err)
		}

		// Convert to map for EventRecord
		var dataMap map[string]interface{}
		if err := json.Unmarshal(eventDataValue, &dataMap); err != nil {
			return fmt.Errorf("cannot convert event data to map: %w", err)
		}

		// Create EventRecord
		record := EventRecord{
			ID:          eventID,
			Slot:        slot,
			TxSignature: txSignature.String(),
			Program:     programID.String(),
			EventType:   eventName,
			Data:        dataMap,
		}

		// Marshal EventRecord
		recordBytes, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("cannot marshal event record: %w", err)
		}

		// Store with event ID as key
		return unprocessedBucket.Put(encodeUint64(eventID), recordBytes)
	}

	if tx == nil {
		return b.db.Update(storeFn)
	}

	if tx, ok := tx.(*bolt.Tx); ok {
		return storeFn(tx)
	}

	return fmt.Errorf("unknown storage transaction type: %T", tx)
}

func (b *BoltStorageHandler) UseTransactions() bool {
	return b.txMode
}

func (b *BoltStorageHandler) ApplyTransaction(
	slotFn func(StorageTransaction) error,
	eventFns []func(StorageTransaction) error) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		for _, fn := range eventFns {
			if err := fn(tx); err != nil {
				return err
			}
		}

		return slotFn(tx)
	})
}

func (b *BoltStorageHandler) GetBlockhashBySlot(slot uint64) (solana.Hash, error) {
	head, err := b.ReadSlot()
	if err != nil {
		return solana.Hash{}, fmt.Errorf("cannot read current slot: %w", err)
	}

	if slot >= head {
		return solana.Hash{}, fmt.Errorf("slot %d has not been processed yet (head is %d)", slot, head)
	}

	var found solana.Hash

	err = b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(blocksBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find blocks bucket")
		}

		for s := slot; s < head; s++ {
			v := bucket.Get(encodeUint64(s))
			if v != nil {
				copy(found[:], v)

				return nil
			}
		}

		return fmt.Errorf("no block hash found at or after slot %d (head is %d)", slot, head)
	})

	return found, err
}

func (b *BoltStorageHandler) GetSlotByBlockhash(hash solana.Hash) (uint64, error) {
	var found uint64

	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(blocksBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find blocks bucket")
		}

		cursor := bucket.Cursor()

		for k, v := cursor.Last(); k != nil; k, v = cursor.Prev() {
			var h solana.Hash

			copy(h[:], v)

			if h == hash {
				found = decodeUint64(k)

				return nil
			}
		}

		return fmt.Errorf("no slot found for block hash %s", hash)
	})

	return found, err
}

func (b *BoltStorageHandler) StoreLatestBlockPoint(tx StorageTransaction, blockPoint BlockPoint) error {
	storeFn := func(tx *bolt.Tx) error {
		bucket := tx.Bucket(latestBlockPointBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find latestBlockPoint bucket")
		}

		bytes, err := json.Marshal(blockPoint)
		if err != nil {
			return fmt.Errorf("could not marshal latest block point: %w", err)
		}

		if err = bucket.Put(latestBlockPointKey, bytes); err != nil {
			return fmt.Errorf("latest block point write error: %w", err)
		}

		return nil
	}

	if tx == nil {
		return b.db.Update(storeFn)
	}

	if tx, ok := tx.(*bolt.Tx); ok {
		return storeFn(tx)
	}

	return fmt.Errorf("unknown storage transaction type: %T", tx)
}

func (b *BoltStorageHandler) GetLatestBlockPoint() (*BlockPoint, error) {
	var result *BlockPoint

	if err := b.db.View(func(tx *bolt.Tx) error {
		if data := tx.Bucket(latestBlockPointBucket).Get(latestBlockPointKey); len(data) > 0 {
			return json.Unmarshal(data, &result)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}

// Retrieves up to N unprocessed events in order (by event ID)
func (b *BoltStorageHandler) GetUnprocessedEvents(limit int) ([]EventRecord, error) {
	var results []EventRecord

	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(unprocessedEventsBucket)
		if bucket == nil {
			return nil // No unprocessed events bucket yet
		}

		cursor := bucket.Cursor()
		count := 0

		// Iterate in order (keys are sorted by default in BoltDB)
		for k, v := cursor.First(); k != nil && count < limit; k, v = cursor.Next() {
			var record EventRecord
			if err := json.Unmarshal(v, &record); err != nil {
				return fmt.Errorf("failed to unmarshal event record: %w", err)
			}

			results = append(results, record)
			count++
		}

		return nil
	})

	return results, err
}

// Moves an event from unprocessed to processed bucket
func (b *BoltStorageHandler) MarkEventAsProcessed(eventID uint64) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		unprocessedBucket := tx.Bucket(unprocessedEventsBucket)
		if unprocessedBucket == nil {
			return fmt.Errorf("unprocessed events bucket not found")
		}

		processedBucket := tx.Bucket(processedEventsBucket)
		if processedBucket == nil {
			return fmt.Errorf("processed events bucket not found")
		}

		// Get event from unprocessed bucket
		eventKey := encodeUint64(eventID)

		eventData := unprocessedBucket.Get(eventKey)
		if eventData == nil {
			return fmt.Errorf("event with ID %d not found in unprocessed bucket", eventID)
		}

		// Move to processed bucket
		if err := processedBucket.Put(eventKey, eventData); err != nil {
			return fmt.Errorf("failed to store event in processed bucket: %w", err)
		}

		// Remove from unprocessed bucket
		if err := unprocessedBucket.Delete(eventKey); err != nil {
			return fmt.Errorf("failed to delete event from unprocessed bucket: %w", err)
		}

		return nil
	})
}

// Returns the number of unprocessed events
func (b *BoltStorageHandler) GetUnprocessedEventCount() (int, error) {
	return b.bucketKeyCount(unprocessedEventsBucket)
}

// Returns the number of processed events
func (b *BoltStorageHandler) GetProcessedEventCount() (int, error) {
	return b.bucketKeyCount(processedEventsBucket)
}

func (b *BoltStorageHandler) bucketKeyCount(name []byte) (int, error) {
	var count int

	err := b.db.View(func(tx *bolt.Tx) error {
		if bucket := tx.Bucket(name); bucket != nil {
			count = bucket.Stats().KeyN
		}

		return nil
	})

	return count, err
}
