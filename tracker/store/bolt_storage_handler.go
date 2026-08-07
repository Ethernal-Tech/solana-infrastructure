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

	// StoreBlock persists a BlockPoint in the per-slot block index. When the
	// block is first discovered via the chain head it is stored with Processed
	// set to false. Once the tracker's catch-up loop fully processes that slot
	// the same method is called again with Processed set to true, updating the
	// existing entry. If the method returns an error, the tracker will terminate
	// immediately.
	StoreBlock(StorageTransaction, BlockPoint) error

	// GetBlockhashBySlot returns the block hash stored for the given slot. If no hash exists for
	// that exact slot (i.e. it was an empty/skipped slot), it walks forward by incrementing the
	// slot number until a hash is found or the current indexing head (ReadSlot) is exceeded, in
	// which case an error is returned.
	GetBlockhashBySlot(uint64) (solana.Hash, error)

	// GetBlockNumberByBlockhash returns the block number (height) stored for the given block hash.
	// If no block exists for that hash, an error is returned.
	GetBlockNumberByBlockhash(solana.Hash) (uint64, error)

	// StoreLatestBlockPoint is invoked by the tracker after every successfully processed block.
	StoreLatestBlockPoint(StorageTransaction, BlockPoint) error

	// GetLatestBlockPoint returns the latest block point data, i.e. block slot and block hash.
	GetLatestBlockPoint() (*BlockPoint, error)

	// GetEventsBySlot returns all tracked events stored for the given slot, from both the
	// unprocessed and processed indexer event buckets.
	GetEventsBySlot(slot uint64) ([]EventRecord, error)

	// GetProcessedTxSignaturesBySlot returns the distinct signatures of the transactions that
	// produced stored events in the given slot, in the order the events were stored. It is
	// derived from the event records, so it only reports transactions that emitted a tracked
	// event; a processed transaction that emitted none is not included.
	GetProcessedTxSignaturesBySlot(slot uint64) ([]solana.Signature, error)

	// StoreEvent is invoked by the tracker after each successfully processed tracked event. In
	// transaction-like mode, the method is not invoked directly but rather wrapped and passed to
	// [ApplyTransaction]. The first argument is a transaction object from the underlying storage
	// backend, see [ApplyTransaction] for more information. The remaining arguments are, in order:
	// the slot number in which the event occurred, the signature of the transaction that generated
	// the event, the public key (address) of the Solana program that emitted the event, the event
	// name as registered in the [ProgramEventSpecs] config, and the deserialized event itself. If
	// the method returns an error, the tracker will terminate immediately.
	StoreEvent(StorageTransaction, uint64, uint64, solana.Signature, solana.PublicKey, string, [32]byte, any) error

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

	TxStorageHandler

	Close() error
}

type TxStorageHandler interface {
	PushUnprocessedTransactions(txPoints []TxPoint) error
	RemoveProcessedTransaction(txSignature solana.Signature) error
	GetAllUnprocessedTransactions() ([]TxPoint, error)
	SetLastProcessedTransaction(txPoint TxPoint) error
	GetLastProcessedTransaction() (TxPoint, error)
	// StoreLatestQueriedTransaction records the newest transaction returned by
	// getSignaturesForAddress, that is, the point new queries resume from.
	StoreLatestQueriedTransaction(txPoint TxPoint) error
	GetLatestQueriedTransaction() (TxPoint, error)
	// FinalizeProcessedTransaction atomically removes txSignature from the front of the
	// unprocessed queue and stores it as the last processed transaction.
	FinalizeProcessedTransaction(txSignature solana.Signature) error
	StoreLatestFinalizedBlockNumber(blockNumber uint64) error
	GetLatestFinalizedBlockNumber() (uint64, error)

	GetEventsByBlockNumber(blockNumber uint64) ([]EventRecord, error)
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
	BlockNumber     uint64                 `json:"block_number"`
	TxSignature     string                 `json:"tx_signature"`
	Program         string                 `json:"program"`
	EventType       string                 `json:"event_type"`
	InnerActionHash [32]byte               `json:"inner_action_hash"`
	Data            map[string]interface{} `json:"data"`
}

type BlockPoint struct {
	BlockSlot   uint64      `json:"slot"`
	BlockHash   solana.Hash `json:"hash"`
	BlockNumber uint64      `json:"number"`
}

// TxPoint pairs a transaction signature with the slot the transaction landed in.
// A zero TxSignature means "none": no transaction has been queried or processed yet.
type TxPoint struct {
	TxSignature solana.Signature `json:"tx_signature"`
	Slot        uint64           `json:"slot"`
}

var (
	slotBucket                       = []byte("slot")
	blocksBucket                     = []byte("blocks")
	blockHashToNumberBucket          = []byte("block_hash_to_number")
	latestBlockPointBucket           = []byte("latestBlockPoint")
	unprocessedEventsBucket          = []byte("unprocessed_events")
	processedEventsBucket            = []byte("processed_events")
	eventIDCounterBucket             = []byte("event_id_counter")
	unprocessedTxSignaturesBucket    = []byte("unprocessed_tx_signatures")
	latestFinalizedBlockNumberBucket = []byte("latest_finalized_block_number")

	currentSlotKey      = []byte("current")
	latestBlockPointKey = []byte("latestBlockPointKey")

	unprocessedTxQueueHeadKey      = []byte("head")
	unprocessedTxQueueTailKey      = []byte("tail")
	unprocessedTxSignaturesListKey = []byte("list") // legacy; migrated on access
	lastProcessedTxSignatureKey    = []byte("last_processed")
	lastQueriedTxSignatureKey      = []byte("last_queried")
	latestFinalizedBlockNumberKey  = []byte("latest_finalized_block_number_key")
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

		_, err = tx.CreateBucketIfNotExists(blockHashToNumberBucket)
		if err != nil {
			return fmt.Errorf("cannot create the blockHashToNumber bucket: %w", err)
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

		_, err = tx.CreateBucketIfNotExists(unprocessedTxSignaturesBucket)
		if err != nil {
			return fmt.Errorf("cannot create the unprocessed tx signatures bucket: %w", err)
		}

		_, err = tx.CreateBucketIfNotExists(latestFinalizedBlockNumberBucket)
		if err != nil {
			return fmt.Errorf("cannot create the latest finalized block number bucket: %w", err)
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

func (b *BoltStorageHandler) StoreBlock(tx StorageTransaction, bp BlockPoint) error {
	storeFn := func(tx *bolt.Tx) error {
		blocks := tx.Bucket(blocksBucket)
		if blocks == nil {
			return fmt.Errorf("cannot find blocks bucket")
		}

		blockHashToNumber := tx.Bucket(blockHashToNumberBucket)
		if blockHashToNumber == nil {
			return fmt.Errorf("cannot find blockHashToNumber bucket")
		}

		data, err := json.Marshal(bp)
		if err != nil {
			return fmt.Errorf("cannot marshal block point: %w", err)
		}

		if err := blocks.Put(encodeUint64(bp.BlockSlot), data); err != nil {
			return fmt.Errorf("cannot persist block point for slot %d: %w", bp.BlockSlot, err)
		}

		if err := blockHashToNumber.Put(bp.BlockHash[:], encodeUint64(bp.BlockNumber)); err != nil {
			return fmt.Errorf("cannot persist block hash index for slot %d: %w", bp.BlockSlot, err)
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

func (b *BoltStorageHandler) StoreEvent(
	tx StorageTransaction,
	slot uint64,
	blockNumber uint64,
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
			ID:              eventID,
			Slot:            slot,
			BlockNumber:     blockNumber,
			InnerActionHash: innerActionHash,
			TxSignature:     txSignature.String(),
			Program:         programID.String(),
			EventType:       eventName,
			Data:            dataMap,
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
	bp, err := b.GetLatestBlockPoint()
	if err != nil {
		return solana.Hash{}, fmt.Errorf("cannot read latest block point: %w", err)
	}

	if bp == nil {
		return solana.Hash{}, fmt.Errorf("no chain head stored yet")
	}

	if slot > bp.BlockSlot {
		return solana.Hash{}, fmt.Errorf("slot %d is beyond chain head (head slot is %d)", slot, bp.BlockSlot)
	}

	var found solana.Hash

	err = b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(blocksBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find blocks bucket")
		}

		for s := slot; ; s-- {
			v := bucket.Get(encodeUint64(s))
			if v == nil {
				if s == 0 {
					break
				}

				continue
			}

			var entry BlockPoint
			if err := json.Unmarshal(v, &entry); err != nil {
				return fmt.Errorf("cannot unmarshal block point at slot %d: %w", s, err)
			}

			found = entry.BlockHash

			return nil
		}

		return fmt.Errorf("no block hash found at or before slot %d", slot)
	})

	return found, err
}

func (b *BoltStorageHandler) GetBlockNumberByBlockhash(hash solana.Hash) (uint64, error) {
	var found uint64

	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(blockHashToNumberBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find blockHashToNumber bucket")
		}

		value := bucket.Get(hash[:])
		if value == nil {
			return fmt.Errorf("no block number found for block hash %s", hash)
		}

		if len(value) != 8 {
			return fmt.Errorf("invalid block number encoding for block hash %s", hash)
		}

		found = decodeUint64(value)

		return nil
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

func (b *BoltStorageHandler) StoreLatestFinalizedBlockNumber(blockNumber uint64) error {
	storeFn := func(tx *bolt.Tx) error {
		bucket := tx.Bucket(latestFinalizedBlockNumberBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find latestFinalizedBlockNumber bucket")
		}

		if err := bucket.Put(latestFinalizedBlockNumberKey, encodeUint64(blockNumber)); err != nil {
			return fmt.Errorf("cannot store latest finalized block number: %w", err)
		}

		return nil
	}

	return b.db.Update(storeFn)
}

func (b *BoltStorageHandler) GetLatestFinalizedBlockNumber() (uint64, error) {
	var result uint64

	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(latestFinalizedBlockNumberBucket)
		if bucket == nil {
			return fmt.Errorf("cannot find latestFinalizedBlockNumber bucket")
		}

		if bnBytes := bucket.Get(latestFinalizedBlockNumberKey); len(bnBytes) > 0 {
			result = decodeUint64(bnBytes)
		}

		return nil
	})

	return result, err
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

func (b *BoltStorageHandler) GetEventsBySlot(slot uint64) ([]EventRecord, error) {
	var results []EventRecord

	err := b.db.View(func(tx *bolt.Tx) error {
		for _, bucketName := range [][]byte{unprocessedEventsBucket, processedEventsBucket} {
			bucket := tx.Bucket(bucketName)
			if bucket == nil {
				continue
			}

			cursor := bucket.Cursor()

			for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
				var record EventRecord
				if err := json.Unmarshal(v, &record); err != nil {
					return fmt.Errorf("failed to unmarshal event record: %w", err)
				}

				if record.Slot == slot {
					results = append(results, record)
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

// GetProcessedTxSignaturesBySlot projects the distinct transaction signatures out of the
// events stored for the given slot. Events are only written after their transaction has been
// processed, so a signature appearing here means that transaction was processed; the converse
// does not hold, since a processed transaction that emitted no tracked event stores no record.
func (b *BoltStorageHandler) GetProcessedTxSignaturesBySlot(slot uint64) ([]solana.Signature, error) {
	records, err := b.GetEventsBySlot(slot)
	if err != nil {
		return nil, err
	}

	seen := make(map[solana.Signature]struct{}, len(records))
	signatures := make([]solana.Signature, 0, len(records))

	for _, record := range records {
		txSignature, err := solana.SignatureFromBase58(record.TxSignature)
		if err != nil {
			return nil, fmt.Errorf("invalid tx signature %q in event %d: %w", record.TxSignature, record.ID, err)
		}

		if _, ok := seen[txSignature]; ok {
			continue
		}

		seen[txSignature] = struct{}{}

		signatures = append(signatures, txSignature)
	}

	return signatures, nil
}

func (b *BoltStorageHandler) GetEventsByBlockNumber(blockNumber uint64) ([]EventRecord, error) {
	var results []EventRecord

	err := b.db.View(func(tx *bolt.Tx) error {
		for _, bucketName := range [][]byte{unprocessedEventsBucket, processedEventsBucket} {
			bucket := tx.Bucket(bucketName)
			if bucket == nil {
				continue
			}

			cursor := bucket.Cursor()

			for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
				var record EventRecord
				if err := json.Unmarshal(v, &record); err != nil {
					return fmt.Errorf("failed to unmarshal event record: %w", err)
				}

				if record.BlockNumber == blockNumber {
					results = append(results, record)
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

// txQueueBounds tracks a FIFO queue stored as encodeUint64(index) -> encodeTxPoint(entry).
// head is the index of the front element; tail is one past the last element.
type txQueueBounds struct {
	head uint64
	tail uint64
}

func (b *BoltStorageHandler) unprocessedTxSignaturesBucket(tx *bolt.Tx) (*bolt.Bucket, error) {
	bucket := tx.Bucket(unprocessedTxSignaturesBucket)
	if bucket == nil {
		return nil, fmt.Errorf("unprocessed tx signatures bucket not found")
	}

	return bucket, nil
}

func loadTxQueueBounds(bucket *bolt.Bucket) (txQueueBounds, error) {
	var bounds txQueueBounds

	if v := bucket.Get(unprocessedTxQueueHeadKey); v != nil {
		bounds.head = decodeUint64(v)
	}

	if v := bucket.Get(unprocessedTxQueueTailKey); v != nil {
		bounds.tail = decodeUint64(v)
	}

	return bounds, nil
}

func storeTxQueueHead(bucket *bolt.Bucket, head uint64) error {
	return bucket.Put(unprocessedTxQueueHeadKey, encodeUint64(head))
}

func storeTxQueueTail(bucket *bolt.Bucket, tail uint64) error {
	return bucket.Put(unprocessedTxQueueTailKey, encodeUint64(tail))
}

// migrateLegacyTxQueueList converts the old single-key JSON list into index-keyed entries.
func (b *BoltStorageHandler) migrateLegacyTxQueueList(bucket *bolt.Bucket) error {
	if bucket.Get(unprocessedTxQueueHeadKey) != nil || bucket.Get(unprocessedTxQueueTailKey) != nil {
		return nil
	}

	data := bucket.Get(unprocessedTxSignaturesListKey)
	if data == nil {
		return nil
	}

	var sigStrings []string
	if err := json.Unmarshal(data, &sigStrings); err != nil {
		return fmt.Errorf("cannot unmarshal legacy unprocessed tx signatures: %w", err)
	}

	var tail uint64

	for i, s := range sigStrings {
		sig, err := solana.SignatureFromBase58(s)
		if err != nil {
			return fmt.Errorf("invalid legacy tx signature at index %d: %w", i, err)
		}

		// the legacy list carried no slot, so migrated entries keep slot 0
		if err := bucket.Put(encodeUint64(tail), encodeTxPoint(TxPoint{TxSignature: sig})); err != nil {
			return fmt.Errorf("cannot migrate legacy tx signature at index %d: %w", i, err)
		}

		tail++
	}

	if err := bucket.Delete(unprocessedTxSignaturesListKey); err != nil {
		return fmt.Errorf("cannot delete legacy unprocessed tx signatures list: %w", err)
	}

	if tail == 0 {
		return nil
	}

	if err := bucket.Put(unprocessedTxQueueTailKey, encodeUint64(tail)); err != nil {
		return err
	}

	return bucket.Put(unprocessedTxQueueHeadKey, encodeUint64(0))
}

// encodeTxPoint serializes a TxPoint as signature bytes followed by the big-endian slot.
func encodeTxPoint(txPoint TxPoint) []byte {
	data := make([]byte, 0, len(txPoint.TxSignature)+8)
	data = append(data, txPoint.TxSignature[:]...)

	return append(data, encodeUint64(txPoint.Slot)...)
}

// txPointFromBucketValue decodes a value written by encodeTxPoint. Values written
// before slots were persisted hold only the signature and decode with slot 0.
func txPointFromBucketValue(data []byte) (TxPoint, error) {
	sigLen := len(solana.Signature{})

	if len(data) != sigLen && len(data) != sigLen+8 {
		return TxPoint{}, fmt.Errorf("invalid tx point length %d", len(data))
	}

	var txPoint TxPoint

	copy(txPoint.TxSignature[:], data[:sigLen])

	if len(data) > sigLen {
		txPoint.Slot = decodeUint64(data[sigLen:])
	}

	return txPoint, nil
}

func (b *BoltStorageHandler) PushUnprocessedTransactions(txPoints []TxPoint) error {
	nonEmpty := make([]TxPoint, 0, len(txPoints))

	for _, txPoint := range txPoints {
		if txPoint.TxSignature != (solana.Signature{}) {
			nonEmpty = append(nonEmpty, txPoint)
		}
	}

	if len(nonEmpty) == 0 {
		return nil
	}

	return b.db.Update(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		if err := b.migrateLegacyTxQueueList(bucket); err != nil {
			return err
		}

		bounds, err := loadTxQueueBounds(bucket)
		if err != nil {
			return err
		}

		for _, txPoint := range nonEmpty {
			if err := bucket.Put(encodeUint64(bounds.tail), encodeTxPoint(txPoint)); err != nil {
				return fmt.Errorf("cannot store unprocessed tx signature: %w", err)
			}

			bounds.tail++
		}

		return storeTxQueueTail(bucket, bounds.tail)
	})
}

// removeProcessedTransactionInBucket pops txSignature off the front of the queue and
// returns the removed entry, including the slot recorded when it was pushed.
func removeProcessedTransactionInBucket(bucket *bolt.Bucket, txSignature solana.Signature) (TxPoint, error) {
	bounds, err := loadTxQueueBounds(bucket)
	if err != nil {
		return TxPoint{}, err
	}

	if bounds.head >= bounds.tail {
		return TxPoint{}, fmt.Errorf("no unprocessed transactions to remove")
	}

	headKey := encodeUint64(bounds.head)

	front, err := txPointFromBucketValue(bucket.Get(headKey))
	if err != nil {
		return TxPoint{}, fmt.Errorf("corrupt unprocessed tx queue at index %d: %w", bounds.head, err)
	}

	if front.TxSignature != txSignature {
		return TxPoint{}, fmt.Errorf(
			"transaction %s is not at the front of the unprocessed queue (front is %s)",
			txSignature, front.TxSignature,
		)
	}

	if err := bucket.Delete(headKey); err != nil {
		return TxPoint{}, fmt.Errorf("cannot remove unprocessed tx signature: %w", err)
	}

	return front, storeTxQueueHead(bucket, bounds.head+1)
}

func setLastProcessedTransactionInBucket(bucket *bolt.Bucket, txPoint TxPoint) error {
	return bucket.Put(lastProcessedTxSignatureKey, encodeTxPoint(txPoint))
}

func (b *BoltStorageHandler) RemoveProcessedTransaction(txSignature solana.Signature) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		if err := b.migrateLegacyTxQueueList(bucket); err != nil {
			return err
		}

		_, err = removeProcessedTransactionInBucket(bucket, txSignature)

		return err
	})
}

func (b *BoltStorageHandler) ensureTxQueueMigrated() error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		return b.migrateLegacyTxQueueList(bucket)
	})
}

func (b *BoltStorageHandler) GetAllUnprocessedTransactions() ([]TxPoint, error) {
	if err := b.ensureTxQueueMigrated(); err != nil {
		return nil, err
	}

	var result []TxPoint

	err := b.db.View(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		bounds, err := loadTxQueueBounds(bucket)
		if err != nil {
			return err
		}

		if bounds.tail < bounds.head {
			return fmt.Errorf("corrupt unprocessed tx queue: tail %d < head %d", bounds.tail, bounds.head)
		}

		count := bounds.tail - bounds.head
		if count == 0 {
			return nil
		}

		result = make([]TxPoint, 0, count)

		for i := bounds.head; i < bounds.tail; i++ {
			txPoint, err := txPointFromBucketValue(bucket.Get(encodeUint64(i)))
			if err != nil {
				return fmt.Errorf("corrupt unprocessed tx queue at index %d: %w", i, err)
			}

			result = append(result, txPoint)
		}

		return nil
	})

	return result, err
}

func (b *BoltStorageHandler) SetLastProcessedTransaction(txPoint TxPoint) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		return setLastProcessedTransactionInBucket(bucket, txPoint)
	})
}

func (b *BoltStorageHandler) FinalizeProcessedTransaction(txSignature solana.Signature) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		if err := b.migrateLegacyTxQueueList(bucket); err != nil {
			return err
		}

		txPoint, err := removeProcessedTransactionInBucket(bucket, txSignature)
		if err != nil {
			return err
		}

		return setLastProcessedTransactionInBucket(bucket, txPoint)
	})
}

func (b *BoltStorageHandler) GetLastProcessedTransaction() (TxPoint, error) {
	return b.getTxPointByKey(lastProcessedTxSignatureKey)
}

func (b *BoltStorageHandler) StoreLatestQueriedTransaction(txPoint TxPoint) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		return bucket.Put(lastQueriedTxSignatureKey, encodeTxPoint(txPoint))
	})
}

func (b *BoltStorageHandler) GetLatestQueriedTransaction() (TxPoint, error) {
	return b.getTxPointByKey(lastQueriedTxSignatureKey)
}

// getTxPointByKey reads a single TxPoint from the tx signatures bucket. A missing
// key yields the zero TxPoint, which callers read as "none recorded yet".
func (b *BoltStorageHandler) getTxPointByKey(key []byte) (TxPoint, error) {
	var txPoint TxPoint

	err := b.db.View(func(tx *bolt.Tx) error {
		bucket, err := b.unprocessedTxSignaturesBucket(tx)
		if err != nil {
			return err
		}

		data := bucket.Get(key)
		if data == nil {
			return nil
		}

		txPoint, err = txPointFromBucketValue(data)

		return err
	})

	return txPoint, err
}
