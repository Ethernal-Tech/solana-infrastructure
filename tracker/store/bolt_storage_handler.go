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
	// StoreBlock persists a BlockPoint in the per-slot block index.
	StoreBlock(StorageTransaction, BlockPoint) error

	// GetBlockhashBySlot returns the block hash stored for the given slot. If no hash exists for
	// that exact slot (i.e. it was an empty/skipped slot), it walks backwards by decrementing the
	// slot number until a hash is found or the existing slots are depleted, in
	// which case an error is returned.
	GetBlockhashBySlot(uint64) (solana.Hash, error)

	// GetBlockNumberByBlockhash returns the block number (height) stored for the given block hash.
	// If no block exists for that hash, an error is returned.
	GetBlockNumberByBlockhash(solana.Hash) (uint64, error)

	// StoreLatestBlockPoint is invoked by the tracker after every successfully processed block.
	StoreLatestBlockPoint(StorageTransaction, BlockPoint) error

	// GetLatestBlockPoint returns the latest block point data, i.e. block slot and block hash.
	GetLatestBlockPoint() (*BlockPoint, error)

	// StoreLatestFinalizedBlockNumber records the block number up to which the tracker has
	// finalized its work, overwriting the previous value. The tracker only ever advances it,
	// but the store itself does not enforce that.
	StoreLatestFinalizedBlockNumber(blockNumber uint64) error

	// GetLatestFinalizedBlockNumber returns the last stored finalized block number, or 0 if none
	// has been stored yet.
	GetLatestFinalizedBlockNumber() (uint64, error)

	// StoreEvent is invoked by the tracker after each successfully processed tracked event.
	StoreEvent(StorageTransaction, uint64, uint64, solana.Signature, solana.PublicKey, string, [32]byte, any) error

	// GetEventsBySlot returns all tracked events stored for the given slot, from both the
	// unprocessed and processed indexer event buckets.
	GetEventsBySlot(slot uint64) ([]EventRecord, error)

	// GetEventsByBlockNumber returns the events emitted by the given block, in the order they
	// were stored, looked up through the block number index rather than by scanning every stored
	// event. Events stored without a block number are not reachable here.
	GetEventsByBlockNumber(blockNumber uint64) ([]EventRecord, error)

	// GetProcessedTxSignaturesBySlot returns the distinct signatures of the transactions that
	// produced stored events in the given slot, in the order the events were stored. It is
	// derived from the event records, so it only reports transactions that emitted a tracked
	// event; a processed transaction that emitted none is not included.
	GetProcessedTxSignaturesBySlot(slot uint64) ([]solana.Signature, error)

	// PushUnprocessedTransactions appends the given transactions to the back of the queue of
	// transactions awaiting processing, keeping the order they are passed in (oldest first).
	// Entries with a zero signature are ignored.
	PushUnprocessedTransactions(txPoints []TxPoint) error

	// GetAllUnprocessedTransactions returns every queued transaction that has not been processed
	// yet, front (oldest) first. An empty result means the queue is drained.
	GetAllUnprocessedTransactions() ([]TxPoint, error)

	// FinalizeProcessedTransaction atomically removes txSignature from the front of the
	// unprocessed queue and stores it as the last processed transaction.
	FinalizeProcessedTransaction(txSignature solana.Signature) error

	// SetLastProcessedTransaction overwrites the last processed transaction marker without
	// touching the unprocessed queue. It is meant for transactions the tracker skips rather than
	// processes; transactions that are actually processed go through
	// [StorageHandler.FinalizeProcessedTransaction].
	SetLastProcessedTransaction(txPoint TxPoint) error

	// GetLastProcessedTransaction returns the transaction the tracker last processed or skipped.
	// A zero TxPoint means none has been recorded yet.
	GetLastProcessedTransaction() (TxPoint, error)

	// StoreLatestQueriedTransaction records the newest transaction returned by
	// getSignaturesForAddress, that is, the point new queries resume from.
	StoreLatestQueriedTransaction(txPoint TxPoint) error

	// GetLatestQueriedTransaction returns the transaction last recorded by
	// [StorageHandler.StoreLatestQueriedTransaction]. A zero TxPoint means no query has been
	// recorded yet, so the next query starts from the newest transaction on chain.
	GetLatestQueriedTransaction() (TxPoint, error)

	// Close releases the underlying storage resources. The handler must not be used afterwards.
	Close() error
}

type BoltStorageHandler struct {
	db *bolt.DB
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
	blocksBucket                     = []byte("blocks")
	blockHashToNumberBucket          = []byte("block_hash_to_number")
	latestBlockPointBucket           = []byte("latestBlockPoint")
	unprocessedEventsBucket          = []byte("unprocessed_events")
	processedEventsBucket            = []byte("processed_events")
	eventIDCounterBucket             = []byte("event_id_counter")
	unprocessedTxSignaturesBucket    = []byte("unprocessed_tx_signatures")
	latestFinalizedBlockNumberBucket = []byte("latest_finalized_block_number")
	eventIDsByBlockNumberBucket      = []byte("event_ids_by_block_number")

	latestBlockPointKey = []byte("latestBlockPointKey")

	unprocessedTxQueueHeadKey      = []byte("head")
	unprocessedTxQueueTailKey      = []byte("tail")
	unprocessedTxSignaturesListKey = []byte("list") // legacy; migrated on access
	lastProcessedTxSignatureKey    = []byte("last_processed")
	lastQueriedTxSignatureKey      = []byte("last_queried")
	latestFinalizedBlockNumberKey  = []byte("latest_finalized_block_number_key")
)

func NewBoltStorageHandler(path string) (*BoltStorageHandler, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot open bolt db: %w", err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
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

		_, err = tx.CreateBucketIfNotExists(eventIDsByBlockNumberBucket)
		if err != nil {
			return fmt.Errorf("cannot create the event IDs by block number bucket: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return &BoltStorageHandler{db}, nil
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
		if err := unprocessedBucket.Put(encodeUint64(eventID), recordBytes); err != nil {
			return fmt.Errorf("cannot persist event record %d: %w", eventID, err)
		}

		// Index the event under its block number, in the same transaction as the record so the
		// two can never disagree
		return appendEventIDForBlockNumber(tx, blockNumber, eventID)
	}

	if tx == nil {
		return b.db.Update(storeFn)
	}

	if tx, ok := tx.(*bolt.Tx); ok {
		return storeFn(tx)
	}

	return fmt.Errorf("unknown storage transaction type: %T", tx)
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

// decodeEventIDs unpacks the value of an eventIDsByBlockNumberBucket entry, which is a plain
// concatenation of big-endian event IDs in the order the events were stored.
func decodeEventIDs(data []byte) ([]uint64, error) {
	if len(data)%8 != 0 {
		return nil, fmt.Errorf("invalid event ID list length %d", len(data))
	}

	eventIDs := make([]uint64, 0, len(data)/8)

	for offset := 0; offset < len(data); offset += 8 {
		eventIDs = append(eventIDs, decodeUint64(data[offset:offset+8]))
	}

	return eventIDs, nil
}

// appendEventIDForBlockNumber adds eventID to the list indexed under blockNumber. A block can
// contain several tracked events, emitted by one transaction or by many, so the index maps a
// block number to every event ID it produced.
func appendEventIDForBlockNumber(tx *bolt.Tx, blockNumber uint64, eventID uint64) error {
	bucket := tx.Bucket(eventIDsByBlockNumberBucket)
	if bucket == nil {
		return fmt.Errorf("event IDs by block number bucket not found")
	}

	key := encodeUint64(blockNumber)
	existing := bucket.Get(key)

	// Get returns memory owned by bolt that the Put below may invalidate, so build a new slice
	updated := make([]byte, 0, len(existing)+8)
	updated = append(updated, existing...)
	updated = append(updated, encodeUint64(eventID)...)

	if err := bucket.Put(key, updated); err != nil {
		return fmt.Errorf("cannot index event %d under block %d: %w", eventID, blockNumber, err)
	}

	return nil
}

// GetEventsByBlockNumber returns the events emitted by the given block, looked up through the
// block number index rather than by scanning every stored event.
//
// Events written before the index existed have no entry, and events written before the block
// number was persisted carry block number 0, so neither is reachable here. That is deliberate:
// the only consumer walks block numbers forward and never revisits a block once it has passed,
// so those records are never queried.
func (b *BoltStorageHandler) GetEventsByBlockNumber(blockNumber uint64) ([]EventRecord, error) {
	var results []EventRecord

	err := b.db.View(func(tx *bolt.Tx) error {
		index := tx.Bucket(eventIDsByBlockNumberBucket)
		if index == nil {
			return fmt.Errorf("event IDs by block number bucket not found")
		}

		data := index.Get(encodeUint64(blockNumber))
		if data == nil {
			return nil
		}

		eventIDs, err := decodeEventIDs(data)
		if err != nil {
			return fmt.Errorf("corrupt event index for block %d: %w", blockNumber, err)
		}

		unprocessed := tx.Bucket(unprocessedEventsBucket)
		processed := tx.Bucket(processedEventsBucket)

		results = make([]EventRecord, 0, len(eventIDs))

		for _, eventID := range eventIDs {
			key := encodeUint64(eventID)

			var raw []byte

			if unprocessed != nil {
				raw = unprocessed.Get(key)
			}

			if raw == nil && processed != nil {
				raw = processed.Get(key)
			}

			if raw == nil {
				return fmt.Errorf("event %d indexed under block %d is missing", eventID, blockNumber)
			}

			var record EventRecord

			if err := json.Unmarshal(raw, &record); err != nil {
				return fmt.Errorf("failed to unmarshal event record %d: %w", eventID, err)
			}

			results = append(results, record)
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

// removeTransactionInBucket pops txSignature off the front of the queue and
// returns the removed entry, including the slot recorded when it was pushed.
func removeTransactionInBucket(bucket *bolt.Bucket, txSignature solana.Signature) (TxPoint, error) {
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

func (b *BoltStorageHandler) GetAllUnprocessedTransactions() ([]TxPoint, error) {
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

		txPoint, err := removeTransactionInBucket(bucket, txSignature)
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
