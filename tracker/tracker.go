package tracker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go/rpc"
	"github.com/hashicorp/go-hclog"

	"github.com/Ethernal-Tech/solana-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/tracker/store"
	binary "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
)

const (
	getSignaturesForAddressMaxLimit = 1000

	// chainHeadTargetBlockCount is the desired number of slots-with-blocks per
	// refresh when near the chain head. Fewer results trigger a proportional wait.
	chainHeadTargetBlockCount = 13
	avgBlockTime              = 400 * time.Millisecond

	emptySlotsWithBlocksOffset = chainHeadTargetBlockCount - 3
)

// EventNotification represents a notification sent on the chEvent channel.
type EventNotification struct {
	// SlotNumber is the number of the slot in which the tracked event was emitted.
	SlotNumber uint64

	// BlockNumber is the number of the block in which the transaction was executed.
	BlockNumber uint64

	// TxSignature is the signature of the transaction that generated the event.
	TxSignature solana.Signature

	// InnerActionHash is the hash of the inner action of the transaction.
	InnerActionHash [32]byte

	// Program is the public key (address) of the Solana program that emitted the tracked event.
	Program solana.PublicKey

	// EventName is the name of the event, as registered in the corresponding ProgramEventSpecs.
	EventName string

	// EventData is the deserialized event payload. The dynamic type of the returned value is a
	// pointer, and the pointed data MUST be treated as read-only. Modifying it may result in a
	// data race. In order to modify it, create a deep copy.
	EventData any
}

type EventSubscriber interface {
	AddEvent(event EventNotification) error
}

type EventTrackerConfig struct {
	RPCEndpoint            string
	Client                 *rpc.Client
	RPCMethodLimitsConfig  *common.RPCMethodLimitsConfig
	TrackedPrograms        map[string]ProgramEventSpecs
	Commitment             string
	Logger                 hclog.Logger
	RetryTimeout           time.Duration
	StartFromSlot          uint64
	BlockRoundingThreshold uint64
	EventSubscriber        EventSubscriber
	DisableRateLimiting    bool
}

type LatestGetBlocksState struct {
	chainHeadSlot             uint64
	queriedBlocksWithSlotsLen int
}

type EventTracker struct {
	client                 *common.MutexRPCClient
	storage                store.StorageHandler
	trackedPrograms        map[solana.PublicKey]ProgramEventSpecs
	commitment             rpc.CommitmentType
	logger                 hclog.Logger
	pollTime               time.Duration
	startFromSlot          uint64
	chainHeadSlot          uint64
	chainHeadSlotOffset    uint64
	lastQueriedTxSignature solana.Signature
	// lastQueriedTxSlot is the slot of the newest finalized signature queried so
	// far, mirrored from storage. It is the durable fallback cursor used when
	// lastQueriedTxSignature can no longer be resolved by the node.
	lastQueriedTxSlot uint64
	// txCursorUnresolvable records that the node cannot resolve
	// lastQueriedTxSignature, so signatures have to be queried from
	// lastQueriedTxSlot instead. The signature itself is kept, because it is still
	// the marker runTransactionPolling compares against the last processed one.
	txCursorUnresolvable   bool
	blockRoundingThreshold uint64
	EventSubscriber        EventSubscriber
	latestGetBlocksState   LatestGetBlocksState
}

func NewEventTracker(config *EventTrackerConfig, storage store.StorageHandler) (*EventTracker, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if storage == nil {
		return nil, fmt.Errorf("storage cannot be nil")
	}

	if config.EventSubscriber == nil {
		return nil, fmt.Errorf("invalid configuration, event subscriber not set. Failed to init Event Tracker")
	}

	if len(config.TrackedPrograms) == 0 {
		return nil, fmt.Errorf("must track at least one program")
	}

	trackedPrograms := make(map[solana.PublicKey]ProgramEventSpecs, len(config.TrackedPrograms))

	for programIDstr, eventSpecs := range config.TrackedPrograms {
		programID, err := solana.PublicKeyFromBase58(programIDstr)
		if err != nil {
			return nil, fmt.Errorf("invalid program ID %s: %w", programIDstr, err)
		}

		trackedPrograms[programID] = eventSpecs
	}

	// Set up RPC client if not provided externally
	if err := setupClientNew(config); err != nil {
		return nil, err
	}

	pollTime := config.RetryTimeout
	if pollTime == 0 {
		pollTime = 500 * time.Millisecond
	}

	commitment := rpc.CommitmentFinalized
	if config.Commitment == "confirmed" {
		commitment = rpc.CommitmentConfirmed
	}

	blockRoundingThreshold := config.BlockRoundingThreshold
	if blockRoundingThreshold == 0 {
		blockRoundingThreshold = 10
	}

	t := &EventTracker{
		client:                 common.NewMutexRPCClient(config.Client, config.RPCMethodLimitsConfig),
		storage:                storage,
		trackedPrograms:        trackedPrograms,
		commitment:             commitment,
		logger:                 config.Logger,
		pollTime:               pollTime,
		chainHeadSlot:          config.StartFromSlot,
		startFromSlot:          config.StartFromSlot,
		chainHeadSlotOffset:    50,
		lastQueriedTxSignature: solana.Signature{},
		blockRoundingThreshold: blockRoundingThreshold,
		EventSubscriber:        config.EventSubscriber,
		latestGetBlocksState: LatestGetBlocksState{
			chainHeadSlot:             config.StartFromSlot,
			queriedBlocksWithSlotsLen: 0,
		},
	}

	return t, nil
}

func (t *EventTracker) Start(ctx context.Context) {
	t.logger.Info("Starting new event tracker")

	if err := t.initialize(ctx); err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to initialize event tracker: %s", err.Error()))

		return
	}

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()
		t.runChainHeadRefresh(ctx)
	}()

	go func() {
		defer wg.Done()
		t.runTransactionPolling(ctx)
	}()

	wg.Wait()
	t.logger.Info("Context done, stopping event tracker")
}

func (t *EventTracker) runChainHeadRefresh(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			idleWait, err := t.refreshChainHead(ctx)
			if err != nil {
				t.logger.Warn(fmt.Sprintf("Failed to refresh chain head: %s", err.Error()))
			}

			if idleWait > 0 {
				t.logger.Debug("Chain head idle, waiting before next refresh",
					"wait", idleWait,
					"chainHeadSlot", t.chainHeadSlot)

				select {
				case <-ctx.Done():
					return
				case <-time.After(idleWait):
				}
			}
		}
	}
}

func (t *EventTracker) runTransactionPolling(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			lastProcessedTxSignature, err := t.storage.GetLastProcessedTransaction()
			if err != nil {
				t.logger.Warn(fmt.Sprintf("Failed to get last processed transaction: %s", err.Error()))

				continue
			}

			// Force tx processing if there are unprocessed tx signatures
			// before querying new transactions
			lastQueriedTxSignature := t.lastQueriedTxSignature
			if lastQueriedTxSignature != lastProcessedTxSignature {
				if err := t.fetchNextGetFullTxBySignature(ctx); err != nil {
					t.logger.Warn(fmt.Sprintf("Failed to fetch next full tx by signature: %s", err.Error()))
				}
			} else {
				for programID := range t.trackedPrograms {
					if err := t.fetchNextGetSignaturesForAddress(ctx, programID, lastQueriedTxSignature); err != nil {
						t.logger.Warn(fmt.Sprintf("Failed to fetch next signatures for address: %s", err.Error()))
					}
				}
			}
		}
	}
}

func (t *EventTracker) getLastUnprocessedTxSignature() (solana.Signature, error) {
	unprocessedTxSignatures, err := t.storage.GetAllUnprocessedTransactions()
	if err != nil {
		return solana.Signature{}, err
	}

	if len(unprocessedTxSignatures) == 0 {
		lastProcessedTxSignature, err := t.storage.GetLastProcessedTransaction()
		if err != nil {
			return solana.Signature{}, err
		}

		if lastProcessedTxSignature == (solana.Signature{}) {
			return solana.Signature{}, nil
		}

		return lastProcessedTxSignature, nil
	}

	return unprocessedTxSignatures[0], nil
}

// Initialization on startup
func (t *EventTracker) initialize(ctx context.Context) error {
	latestBlockPoint, err := t.storage.GetLatestBlockPoint()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get latest block point: %s", err.Error()))

		return err
	}

	if latestBlockPoint != nil && latestBlockPoint.BlockSlot > t.chainHeadSlot {
		t.chainHeadSlot = latestBlockPoint.BlockSlot
	}

	unprocessedTxSignatures, err := t.storage.GetAllUnprocessedTransactions()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get all unprocessed transactions: %s", err.Error()))

		return err
	}

	lastProcessedTxSignature, err := t.storage.GetLastProcessedTransaction()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get last processed transaction: %s", err.Error()))

		return err
	}

	if len(unprocessedTxSignatures) > 0 {
		t.lastQueriedTxSignature = unprocessedTxSignatures[len(unprocessedTxSignatures)-1]
	} else {
		// If there are no unprocessed tx signatures, use the last processed tx signature
		// as the last queried tx signature so we can continue querying for new transactions
		// If empty it's a first run, so we need to start from the beginning
		if lastProcessedTxSignature != (solana.Signature{}) {
			t.lastQueriedTxSignature = lastProcessedTxSignature
		} else {
			t.lastQueriedTxSignature = solana.Signature{}
		}
	}

	lastQueriedTxSlot, err := t.storage.GetLastQueriedTxSlot()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get last queried tx slot: %s", err.Error()))

		return err
	}

	t.lastQueriedTxSlot = lastQueriedTxSlot

	if err := t.bootstrapLastQueriedTxSlot(); err != nil {
		return err
	}

	t.discardUnresolvableTxCursor(ctx)

	return nil
}

// bootstrapLastQueriedTxSlot derives an initial slot watermark for storage that
// has none, which is the case for anything that ran before the watermark was
// introduced, and for a tracker whose signature cursor went unresolvable before
// it ever completed a query.
//
// The newest stored event's slot is used. Transactions are processed in ascending
// slot order, so every event that has already been stored comes from a slot at or
// below it, and re-querying from above it cannot miss an event. Transactions in
// between that emitted no tracked event may be fetched again, which stores
// nothing and is therefore harmless.
func (t *EventTracker) bootstrapLastQueriedTxSlot() error {
	if t.lastQueriedTxSlot > 0 {
		return nil
	}

	latestEventSlot, err := t.storage.GetLatestEventSlot()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get latest event slot: %s", err.Error()))

		return err
	}

	if latestEventSlot == 0 {
		return nil
	}

	if err := t.storage.SetLastQueriedTxSlot(latestEventSlot); err != nil {
		return fmt.Errorf("failed to set last queried tx slot: %w", err)
	}

	t.lastQueriedTxSlot = latestEventSlot

	t.logger.Info("No last queried tx slot stored, seeded it from the newest stored event",
		"slot", latestEventSlot)

	return nil
}

// discardUnresolvableTxCursor probes whether the signature cursor restored from
// storage can still be resolved by the node, and drops it if it cannot. Checking
// once on startup is cheaper than discovering it mid-poll, because
// getSignaturesForAddress rejects an unresolvable until/before cursor outright
// (see IsCursorNotFoundErr). Dropping the cursor makes the tracker resume from
// the slot watermark instead.
//
// The cursor is kept whenever the outcome is inconclusive: a probe that fails
// for any other reason (a transport error, an unhealthy node), or a watermark
// that was never persisted - without a watermark there is nothing to fall back
// to, and resuming from startFromSlot could re-ingest a large slot range.
func (t *EventTracker) discardUnresolvableTxCursor(ctx context.Context) {
	if t.lastQueriedTxSignature == (solana.Signature{}) || t.lastQueriedTxSlot == 0 {
		return
	}

	statuses, err := t.client.GetSignatureStatuses(ctx, true, t.lastQueriedTxSignature)
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to check last queried tx signature status: %s", err.Error()))

		return
	}

	// a signature the node cannot resolve comes back as a null status
	if statuses == nil || len(statuses.Value) == 0 || statuses.Value[0] != nil {
		return
	}

	t.logger.Warn("Last queried tx signature is not resolvable by the node, "+
		"resuming from the last queried tx slot",
		"signature", t.lastQueriedTxSignature.String(),
		"slot", t.lastQueriedTxSlot)

	t.txCursorUnresolvable = true
}

// txSlotFloor is the lowest slot the tracker still accepts signatures from when
// it has no usable signature cursor. Signatures at or above it have not been
// queried yet: the watermark slot itself was fully ingested (the whole page it
// arrived in was), so the floor sits one slot above it.
func (t *EventTracker) txSlotFloor() uint64 {
	if t.lastQueriedTxSlot == 0 {
		return t.startFromSlot
	}

	if floor := t.lastQueriedTxSlot + 1; floor > t.startFromSlot {
		return floor
	}

	return t.startFromSlot
}

// Fetches tx and does processing
func (t *EventTracker) fetchNextGetFullTxBySignature(ctx context.Context) error {
	txSignature, err := t.getLastUnprocessedTxSignature()
	if err != nil {
		return err
	}

	lastProcessedTxSignature, err := t.storage.GetLastProcessedTransaction()
	if err != nil {
		return err
	}

	if txSignature == (solana.Signature{}) || txSignature == lastProcessedTxSignature {
		return nil
	}

	t.logger.Debug("Fetching next full tx by signature", "tx signature", txSignature.String())

	transactionResponse, err := t.client.GetTransaction(ctx, txSignature, &rpc.GetTransactionOpts{
		Commitment:                     t.commitment,
		MaxSupportedTransactionVersion: new(uint64),
	})
	if err != nil {
		return err
	}

	if transactionResponse == nil {
		return fmt.Errorf("transaction not found")
	}

	if transactionResponse.Meta == nil {
		return fmt.Errorf("cannot read meta data for the transaction")
	}

	if len(transactionResponse.Meta.LogMessages) == 0 {
		t.logger.Warn("No log messages found for the transaction")

		return nil
	}

	transaction, err := transactionResponse.Transaction.GetTransaction()
	if err != nil {
		return fmt.Errorf("failed to get transaction: %w", err)
	}

	// Processing loop:
	for _, instruction := range transaction.Message.Instructions {
		if int(instruction.ProgramIDIndex) >= len(transaction.Message.AccountKeys) {
			t.logger.Warn(fmt.Sprintf("Invalid ProgramIDIndex in transaction %d (only %d accounts)",
				instruction.ProgramIDIndex, len(transaction.Message.AccountKeys)))

			continue
		}

		programID := transaction.Message.AccountKeys[instruction.ProgramIDIndex]

		var innerActionHash [32]byte

		for _, log := range transactionResponse.Meta.LogMessages {
			if !strings.Contains(log, "Program data: ") {
				continue
			}

			// Extract base64 data
			dataStart := strings.Index(log, "Program data: ")
			if dataStart == -1 {
				continue
			}

			base64Data := log[dataStart+14:] // len("Program data: ") = 14
			base64Data = strings.TrimSpace(base64Data)
			base64Data = strings.TrimRight(base64Data, "=") // Remove padding for RawStdEncoding

			decoded, err := base64.RawStdEncoding.DecodeString(base64Data)
			if err != nil {
				return fmt.Errorf("failed to decode log: %w", err)
			}

			parsed, name, err := t.parseEvent(decoded, programID)
			if err != nil {
				t.logger.Warn(fmt.Sprintf("Failed to parse event: %s", err.Error()))

				continue
			}

			if parsed == nil {
				continue
			}

			// We have to recreate the payload to get the inner action hash
			// that is the only way to map the payload hash from smart contract that oracle is expecting
			// to the batch that was actually executed by relayer within this tx
			if name == "TransactionExecutedEvent" {
				ed25519Data, err := FindEd25519InstructionData(
					transaction.Message.Instructions,
					transaction.Message.AccountKeys,
				)
				if err != nil {
					t.logger.Warn(fmt.Sprintf(
						"TransactionExecutedEvent in tx but no ed25519 instruction: %s",
						err.Error()))

					continue
				}

				payload, err := ExtractSolanaPayloadFromEd25519(ed25519Data)
				if err != nil {
					t.logger.Warn(fmt.Sprintf(
						"Failed to extract solana payload from ed25519 instruction: %s", err.Error()))

					continue
				}

				payloadBytes, err := payload.Marshal()
				if err != nil {
					t.logger.Warn(fmt.Sprintf("Failed to marshal payload: %s", err.Error()))

					continue
				}

				innerActionHash = sha256.Sum256(payloadBytes)
			}

			block, err := ExecuteWithRetry(ctx, func(ctx context.Context) (*rpc.GetBlockResult, error) {
				result, err := t.client.GetBlockWithOpts(ctx, transactionResponse.Slot, &rpc.GetBlockOpts{
					TransactionDetails:             rpc.TransactionDetailsNone,
					MaxSupportedTransactionVersion: new(uint64),
					Commitment:                     t.commitment,
				})
				if err != nil {
					return nil, ErrRetryTryAgain
				}

				return result, nil
			}, WithRetryCount(10), WithRetryWaitTime(t.pollTime))
			if err != nil {
				return fmt.Errorf("failed to get block for event at slot %d: %w", transactionResponse.Slot, err)
			}

			if block == nil {
				return fmt.Errorf("no block found at slot %d for transaction %s", transactionResponse.Slot, txSignature.String())
			}

			if block.BlockHeight != nil {
				err = t.storage.StoreLatestFinalizedBlockNumber(*block.BlockHeight)
				if err != nil {
					return fmt.Errorf("failed to store latest finalized block number for transaction %s: %w",
						txSignature.String(), err)
				}
			} else {
				return fmt.Errorf("no block height found for block at slot %d for transaction %s",
					transactionResponse.Slot, txSignature.String())
			}

			event := EventNotification{
				SlotNumber:      transactionResponse.Slot,
				BlockNumber:     *block.BlockHeight,
				TxSignature:     txSignature,
				InnerActionHash: innerActionHash,
				Program:         programID,
				EventName:       name,
				EventData:       parsed,
			}

			err = t.EventSubscriber.AddEvent(event)
			if err != nil {
				return fmt.Errorf("failed to add event: %w", err)
			}

			t.logger.Info(fmt.Sprintf("Event of type %s emitted by %s at slot %d", name, programID, transactionResponse.Slot))

			err = t.storage.StoreEvent(
				nil,
				event.SlotNumber,
				event.BlockNumber,
				event.TxSignature,
				event.Program,
				event.EventName,
				event.InnerActionHash,
				event.EventData,
			)
			if err != nil {
				return fmt.Errorf("failed to store event: %w", err)
			}
		}
	}

	err = t.storage.FinalizeProcessedTransaction(txSignature)
	if err != nil {
		return fmt.Errorf("failed to finalize processed transaction: %w", err)
	}

	return nil
}

// Helper function to get signatures for an address
// If the last queried tx signature is empty, we query from the beginning
// If it's not empty, we query until the last queried tx signature
// Passing solana.Signature{} doens't work
func (t *EventTracker) getSignaturesForAddressHelper(
	ctx context.Context, programID solana.PublicKey,
	before *solana.Signature,
	until *solana.Signature,
) ([]*rpc.TransactionSignature, error) {
	opts := &rpc.GetSignaturesForAddressOpts{
		Commitment: t.commitment,
	}

	if before != nil && *before != (solana.Signature{}) {
		opts.Before = *before
	}

	if until != nil && *until != (solana.Signature{}) {
		opts.Until = *until
	}

	return t.client.GetSignaturesForAddressWithOpts(ctx, programID, opts)
}

func (t *EventTracker) fetchNextGetSignaturesForAddress(
	ctx context.Context, programID solana.PublicKey, lastQueriedTxSignature solana.Signature,
) error {
	t.logger.Debug("Fetching new transactions", "last queried tx signature", lastQueriedTxSignature.String())

	latestFinalizedBlockNumber, err := t.client.GetBlockHeight(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return err
	}

	var txSignatures []*rpc.TransactionSignature

	// No signature cursor to page from: either the first run, or the cursor was
	// found to be unresolvable by the node. Page by slot instead.
	useSlotFloor := lastQueriedTxSignature == (solana.Signature{}) || t.txCursorUnresolvable

	if useSlotFloor {
		txSignatures, err = t.getSignaturesFromSlotFloor(ctx, programID)
		if err != nil {
			return err
		}
	} else {
		// Query until the last processed signature
		txSignatures, err = t.getSignaturesForAddressHelper(ctx, programID, nil, &lastQueriedTxSignature)
		if err != nil {
			if !IsCursorNotFoundErr(err) {
				return err
			}

			if t.lastQueriedTxSlot == 0 {
				return fmt.Errorf(
					"last queried tx signature %s cannot be resolved and no slot watermark is stored to resume from: %w",
					lastQueriedTxSignature.String(), err)
			}

			t.logger.Warn("Last queried tx signature is no longer resolvable, falling back to the slot watermark",
				"signature", lastQueriedTxSignature.String(),
				"slotFloor", t.txSlotFloor(),
				"err", err.Error())

			// Stop paging from the dead cursor, so subsequent polls do not retry it
			t.txCursorUnresolvable = true
			useSlotFloor = true

			txSignatures, err = t.getSignaturesFromSlotFloor(ctx, programID)
			if err != nil {
				return err
			}
		} else if len(txSignatures) == getSignaturesForAddressMaxLimit {
			// We have to fetch what was before the last processed signature
			// since the initial query returned the max limit of signatures
			signatures, err := t.catchUpLoop(ctx, programID, lastQueriedTxSignature,
				txSignatures[len(txSignatures)-1].Signature)
			if err != nil {
				return err
			}

			txSignatures = append(txSignatures, signatures...)
		}
	}

	if len(txSignatures) == 0 {
		t.logger.Debug("No new transactions found, setting new finalized block number", "number", latestFinalizedBlockNumber)

		err = t.storage.StoreLatestFinalizedBlockNumber(latestFinalizedBlockNumber)
		if err != nil {
			return fmt.Errorf("failed to store latest finalized block number: %w", err)
		}

		return nil
	}

	t.logger.Debug("Fetched new transactions",
		"count", len(txSignatures),
		"last queried tx signature", lastQueriedTxSignature.String())

	// Signatures queried by slot can overlap what is already queued, because the
	// slot floor can sit below signatures that were already fetched: it is seeded
	// from the newest stored event, which lags transactions that are queued but not
	// processed yet, it only tracks finalized signatures when running at confirmed
	// commitment, and it is persisted after the queue is written. Skip the overlap,
	// the queue does not deduplicate on push.
	var alreadyQueued map[solana.Signature]struct{}

	if useSlotFloor {
		alreadyQueued, err = t.getQueuedTxSignatures()
		if err != nil {
			return err
		}
	}

	// Since signatures are queried in reverse order, we need to store them in
	// reverse order to get the correct order for processing
	unprocessedTxSignatures := make([]solana.Signature, 0, len(txSignatures))
	skippedAsQueued := false

	for i := len(txSignatures) - 1; i >= 0; i-- {
		// If there is no usable signature cursor, we need to check if the tx slot is
		// greater than the slot floor, if it is not, we skip the tx
		// important for the first run and after the signature cursor was dropped
		if useSlotFloor {
			if t.txSlotFloor() > txSignatures[i].Slot {
				continue
			}

			if _, ok := alreadyQueued[txSignatures[i].Signature]; ok {
				skippedAsQueued = true

				continue
			}
		}

		unprocessedTxSignatures = append(unprocessedTxSignatures, txSignatures[i].Signature)
	}

	// If we skipped all the txs, we need to set the last queried tx signature to the newest one
	// and set the last processed transaction to the latest one
	if len(unprocessedTxSignatures) == 0 {
		t.setLastQueriedTxSignature(txSignatures[0].Signature)

		// Signatures skipped because they are already queued are not processed yet,
		// so the last processed marker must stay where it is - it is what tells
		// runTransactionPolling that the queue still has to be drained
		if !skippedAsQueued {
			err = t.storage.SetLastProcessedTransaction(txSignatures[0].Signature)
			if err != nil {
				return fmt.Errorf("failed to set last processed transaction on start: %w", err)
			}
		}

		return t.advanceLastQueriedTxSlot(txSignatures)
	}

	err = t.storage.PushUnprocessedTransactions(unprocessedTxSignatures)
	if err != nil {
		return fmt.Errorf("failed to push unprocessed transactions: %w", err)
	}

	t.setLastQueriedTxSignature(txSignatures[0].Signature)

	t.logger.Debug("Fetched new transactions", "count",
		len(txSignatures), "last queried tx signature", txSignatures[0].Signature.String())

	return t.advanceLastQueriedTxSlot(txSignatures)
}

// setLastQueriedTxSignature moves the signature cursor to a signature that was
// just returned by the node, which makes it resolvable again, so paging from it
// can resume.
func (t *EventTracker) setLastQueriedTxSignature(txSignature solana.Signature) {
	t.lastQueriedTxSignature = txSignature
	t.txCursorUnresolvable = false
}

// advanceLastQueriedTxSlot moves the persisted slot watermark up to the slot of
// the newest finalized signature in txSignatures, which must be ordered newest
// first, as getSignaturesForAddress returns it.
//
// Only finalized signatures count. A confirmed-but-not-finalized transaction can
// still be dropped on a fork, and a watermark pointing past dropped transactions
// would make the fallback path skip them. Lagging behind the newest signature is
// safe in the other direction: the fallback may re-query a few signatures that
// were already ingested.
func (t *EventTracker) advanceLastQueriedTxSlot(txSignatures []*rpc.TransactionSignature) error {
	for _, txSignature := range txSignatures {
		// When querying at finalized commitment every returned signature is
		// finalized, whether or not the node populates confirmationStatus
		if t.commitment != rpc.CommitmentFinalized &&
			txSignature.ConfirmationStatus != rpc.ConfirmationStatusFinalized {
			continue
		}

		if txSignature.Slot <= t.lastQueriedTxSlot {
			return nil
		}

		if err := t.storage.SetLastQueriedTxSlot(txSignature.Slot); err != nil {
			return fmt.Errorf("failed to set last queried tx slot: %w", err)
		}

		t.lastQueriedTxSlot = txSignature.Slot

		return nil
	}

	return nil
}

// getQueuedTxSignatures returns the signatures currently waiting in the
// unprocessed queue, plus the last processed one, as a lookup set. Signatures
// that were processed and dropped from the queue earlier are not tracked, so a
// re-query reaching further back than the queue can still produce duplicates.
func (t *EventTracker) getQueuedTxSignatures() (map[solana.Signature]struct{}, error) {
	unprocessedTxSignatures, err := t.storage.GetAllUnprocessedTransactions()
	if err != nil {
		return nil, fmt.Errorf("failed to get all unprocessed transactions: %w", err)
	}

	lastProcessedTxSignature, err := t.storage.GetLastProcessedTransaction()
	if err != nil {
		return nil, fmt.Errorf("failed to get last processed transaction: %w", err)
	}

	queued := make(map[solana.Signature]struct{}, len(unprocessedTxSignatures)+1)

	for _, txSignature := range unprocessedTxSignatures {
		queued[txSignature] = struct{}{}
	}

	if lastProcessedTxSignature != (solana.Signature{}) {
		queued[lastProcessedTxSignature] = struct{}{}
	}

	return queued, nil
}

// getSignaturesFromSlotFloor fetches every signature for programID down to
// txSlotFloor, for when there is no usable signature cursor to page from. Paging
// stops as soon as a page reaches below the floor, so history older than the
// floor is never walked.
func (t *EventTracker) getSignaturesFromSlotFloor(
	ctx context.Context, programID solana.PublicKey,
) ([]*rpc.TransactionSignature, error) {
	slotFloor := t.txSlotFloor()

	t.logger.Debug("Fetching new transactions by slot", "slot floor", slotFloor)

	var (
		result []*rpc.TransactionSignature
		before *solana.Signature
	)

	for {
		txSignatures, err := t.getSignaturesForAddressHelper(ctx, programID, before, nil)
		if err != nil {
			return nil, err
		}

		if len(txSignatures) == 0 {
			return result, nil
		}

		reachedFloor := false

		for _, txSignature := range txSignatures {
			if txSignature.Slot < slotFloor {
				reachedFloor = true

				break
			}

			result = append(result, txSignature)
		}

		if reachedFloor || len(txSignatures) < getSignaturesForAddressMaxLimit {
			return result, nil
		}

		before = &txSignatures[len(txSignatures)-1].Signature
	}
}

func (t *EventTracker) catchUpLoop(
	ctx context.Context,
	programID solana.PublicKey,
	lastQueried solana.Signature,
	beforeStart solana.Signature,
) (signatures []*rpc.TransactionSignature, err error) {
	t.logger.Debug("Catching up loop", "last processed tx signature", beforeStart.String())

	before := beforeStart
	until := lastQueried

	for {
		txSignatures, err := ExecuteWithRetry(ctx, func(ctx context.Context) ([]*rpc.TransactionSignature, error) {
			signatures, err := t.getSignaturesForAddressHelper(ctx, programID, &before, &until)
			if err != nil {
				return nil, ErrRetryTryAgain
			}

			return signatures, nil
		}, WithRetryCount(10), WithRetryWaitTime(t.pollTime))
		if err != nil {
			return nil, err
		}

		signatures = append(signatures, txSignatures...)

		if len(txSignatures) < getSignaturesForAddressMaxLimit {
			break
		}

		before = txSignatures[len(txSignatures)-1].Signature
	}

	t.logger.Debug("Caught up old transactions", "count", len(signatures))

	return signatures, nil
}

func (t *EventTracker) parseEvent(eventData []byte, programID solana.PublicKey) (any, string, error) {
	decoder := binary.NewBorshDecoder(eventData)

	discriminator, err := decoder.ReadDiscriminator()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get event discriminator: %w", err)
	}

	trackedEvents, ok := t.trackedPrograms[programID] // checked existence of programID in processBlock
	if !ok {
		t.logger.Warn(fmt.Sprintf("Program %s not found in tracked programs", programID))

		return nil, "", nil
	}

	for _, event := range trackedEvents {
		if event.discriminant == discriminator {
			value := reflect.New(reflect.ValueOf(event.eventType).Type()).Interface()

			deserValue, ok := value.(interface {
				UnmarshalWithDecoder(decoder *binary.Decoder) (err error)
			})

			if !ok {
				return nil, "", fmt.Errorf(
					"event type %T for event %s (program %s) does not implement UnmarshalWithDecoder method",
					value, event.name, programID)
			}

			if err := deserValue.UnmarshalWithDecoder(decoder); err != nil {
				return nil, "", fmt.Errorf(
					"failed to unmarshal event %s (discriminator: %x, program: %s): %w",
					event.name, discriminator, programID, err)
			}

			return deserValue, event.name, nil
		}
	}

	// No matching discriminator found - this is normal if the log contains
	// data that doesn't match any tracked event types (e.g., program logs)
	return nil, "", nil
}

// setupClient initialises config.Client if it is not already set.
func setupClientNew(config *EventTrackerConfig) error {
	if config.Client != nil {
		return nil
	}

	if config.RPCEndpoint == "" {
		return fmt.Errorf("either config.Client or config.RPCEndpoint must be set")
	}

	// if rate limiting is disabled, we use the default RPC client
	if config.DisableRateLimiting {
		config.Client = rpc.New(config.RPCEndpoint)

		return nil
	}

	globalRPSLimit := 7
	if config.RPCMethodLimitsConfig != nil {
		globalRPSLimit = config.RPCMethodLimitsConfig.GlobalRPSLimit
	}

	config.Client = common.NewRateLimitedRPCClient(config.RPCEndpoint, globalRPSLimit, config.Logger)

	return nil
}

// Helper function to get slots to query blocks
// We need to query blocks at slots that are multiples of the threshold
// and the next slot is not a multiple of the threshold
func getSlotsToQueryBlocks(slotsWithBlocks []uint64, threshold uint64) []uint64 {
	result := make([]uint64, 0)

	for i := 0; i < len(slotsWithBlocks); i++ {
		if slotsWithBlocks[i]%threshold == 0 {
			result = append(result, slotsWithBlocks[i])
		} else if i+1 < len(slotsWithBlocks) &&
			slotsWithBlocks[i+1]/threshold != slotsWithBlocks[i]/threshold &&
			slotsWithBlocks[i+1]%threshold != 0 { // next will add itself, don't add current
			result = append(result, slotsWithBlocks[i])
		}
	}

	return result
}

// chainHeadCatchUpWait returns how long to wait before the next refresh when
// GetBlocks returned fewer slots-with-blocks than the target batch size.
func chainHeadCatchUpWait(blocksWithSlots int) time.Duration {
	if blocksWithSlots >= chainHeadTargetBlockCount {
		return 0
	}

	slotsNeeded := chainHeadTargetBlockCount - blocksWithSlots

	return time.Duration(slotsNeeded) * avgBlockTime
}

// refreshChainHead fetches the block at the given slot and persists it as the
// latest block point. If the slot has no block (skipped/empty), the update is
// silently skipped and the previously stored chain head remains valid.
// Only storage write errors are returned; RPC/fetch failures are non-fatal.
// When GetBlocks returns fewer than chainHeadTargetBlockCount slots, the
// returned idleWait backs off proportionally so we do not poll in a tight loop
// near the chain head.
func (t *EventTracker) refreshChainHead(ctx context.Context) (idleWait time.Duration, err error) {
	t.logger.Debug(fmt.Sprintf("Refreshing chain head to slot %d", t.chainHeadSlot))

	startSlot := t.chainHeadSlot
	endSlot := t.chainHeadSlot + t.chainHeadSlotOffset

	slotsWithBlocks, err := t.client.GetBlocks(ctx, startSlot, &endSlot, rpc.CommitmentConfirmed)
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to fetch blocks with limit to refresh head %d: %s", endSlot, err.Error()))

		return 0, nil
	}

	t.logger.Debug(fmt.Sprintf("Blocks with limit %d at slot %d: %d",
		t.chainHeadSlotOffset, endSlot, len(slotsWithBlocks)), "slotsWithBlocks", slotsWithBlocks)

	state := LatestGetBlocksState{
		chainHeadSlot:             startSlot,
		queriedBlocksWithSlotsLen: len(slotsWithBlocks),
	}

	if state == t.latestGetBlocksState {
		return t.unstickChainHead(slotsWithBlocks)
	}

	t.latestGetBlocksState = state

	if len(slotsWithBlocks) == 0 {
		return chainHeadCatchUpWait(0), nil
	}

	var bp store.BlockPoint

	slotsToQueryBlocks := getSlotsToQueryBlocks(slotsWithBlocks, t.blockRoundingThreshold)

	t.logger.Debug("Slots to query blocks", "count", len(slotsToQueryBlocks), "slots", slotsToQueryBlocks)

	for _, slot := range slotsToQueryBlocks {
		if slot == t.chainHeadSlot {
			continue
		}

		block, err := ExecuteWithRetry(ctx, func(ctx context.Context) (*rpc.GetBlockResult, error) {
			result, err := t.client.GetBlockWithOpts(ctx, slot, &rpc.GetBlockOpts{
				TransactionDetails:             rpc.TransactionDetailsNone,
				MaxSupportedTransactionVersion: new(uint64),
				Commitment:                     rpc.CommitmentConfirmed,
			})
			if err != nil {
				return nil, ErrRetryTryAgain
			}

			return result, nil
		}, WithRetryCount(10), WithRetryWaitTime(t.pollTime))
		if err != nil {
			t.logger.Warn(fmt.Sprintf("Failed to fetch block at slot %d: %s", slot, err.Error()))

			return 0, fmt.Errorf("no block found at slot %d", slot)
		}

		if block == nil {
			return 0, fmt.Errorf("get block returned nil at slot %d", slot)
		}

		var blockNumber uint64
		if block.BlockHeight != nil {
			blockNumber = *block.BlockHeight
		} else {
			t.logger.Warn(fmt.Sprintf("No block height found for block at slot %d", slot))
		}

		bp = store.BlockPoint{
			BlockSlot:   slot,
			BlockHash:   block.Blockhash,
			BlockNumber: blockNumber,
		}

		if err := t.storage.StoreBlock(nil, bp); err != nil {
			return 0, err
		}

		t.logger.Debug("Stored block",
			"slot", slot,
			"hash", block.Blockhash,
			"number", blockNumber)

		t.chainHeadSlot = slot
	}

	if bp.BlockSlot > 0 {
		return chainHeadCatchUpWait(len(slotsWithBlocks)), t.storage.StoreLatestBlockPoint(nil, bp)
	}

	return chainHeadCatchUpWait(len(slotsWithBlocks)), nil
}

// unstickChainHead force-advances the chain head when two consecutive GetBlocks
// calls returned the identical window. That happens when the trailing group of
// slots-with-blocks is never closed by a following block (e.g. an outage skipped
// the rest of the window), so getSlotsToQueryBlocks only ever returns the current
// chain head and refreshChainHead makes no progress.
func (t *EventTracker) unstickChainHead(
	slotsWithBlocks []uint64,
) (idleWait time.Duration, err error) {
	// In case of the outage we advance with this
	newChainHeadSlot := t.chainHeadSlot + emptySlotsWithBlocksOffset

	if len(slotsWithBlocks) > 1 {
		// resume from the last slot we know has a block
		newChainHeadSlot = slotsWithBlocks[len(slotsWithBlocks)-1]
	}

	t.logger.Warn("Chain head stuck, force advancing",
		"from", t.chainHeadSlot,
		"to", newChainHeadSlot,
		"slotsWithBlocks", slotsWithBlocks)

	t.chainHeadSlot = newChainHeadSlot
	t.latestGetBlocksState = LatestGetBlocksState{chainHeadSlot: newChainHeadSlot}

	return 0, nil
}
