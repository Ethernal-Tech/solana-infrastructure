package tracker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"reflect"
	"slices"
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
	chainHeadTargetBlockCount  = 13
	emptySlotsWithBlocksOffset = chainHeadTargetBlockCount - 3
	avgBlockTime               = 400 * time.Millisecond
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

	if err := t.initialize(); err != nil {
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
			lastProcessed, err := t.storage.GetLastProcessedTransaction()
			if err != nil {
				t.logger.Warn(fmt.Sprintf("Failed to get last processed transaction: %s", err.Error()))

				continue
			}

			lastQueried, err := t.storage.GetLatestQueriedTransaction()
			if err != nil {
				t.logger.Warn(fmt.Sprintf("Failed to get last queried transaction: %s", err.Error()))

				continue
			}

			// Force tx processing if there are unprocessed tx signatures
			// before querying new transactions
			if lastQueried.TxSignature != lastProcessed.TxSignature {
				if err := t.fetchNextGetFullTxBySignature(ctx); err != nil {
					t.logger.Warn(fmt.Sprintf("Failed to fetch next full tx by signature: %s", err.Error()))
				}
			} else {
				for programID := range t.trackedPrograms {
					if err := t.fetchNextGetSignaturesForAddress(ctx, programID, lastQueried); err != nil {
						t.logger.Warn(fmt.Sprintf("Failed to fetch next signatures for address: %s", err.Error()))
					}
				}
			}
		}
	}
}

func (t *EventTracker) getLastUnprocessedTxSignature() (store.TxPoint, error) {
	unprocessedTxPoints, err := t.storage.GetAllUnprocessedTransactions()
	if err != nil {
		return store.TxPoint{}, err
	}

	if len(unprocessedTxPoints) == 0 {
		lastProcessed, err := t.storage.GetLastProcessedTransaction()
		if err != nil {
			return store.TxPoint{}, err
		}

		if lastProcessed.TxSignature == (solana.Signature{}) {
			return store.TxPoint{}, nil
		}

		return lastProcessed, nil
	}

	return unprocessedTxPoints[0], nil
}

// Initialization on startup
func (t *EventTracker) initialize() error {
	latestBlockPoint, err := t.storage.GetLatestBlockPoint()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get latest block point: %s", err.Error()))

		return err
	}

	if latestBlockPoint != nil && latestBlockPoint.BlockSlot > t.chainHeadSlot {
		t.chainHeadSlot = latestBlockPoint.BlockSlot
	}

	return t.backfillLatestQueriedTransaction()
}

// backfillLatestQueriedTransaction populates the latest queried transaction for
// databases written before it was persisted, where the record is missing but the
// tracker has already made progress. Without it, the first query would resume from
// the beginning of the program history. The value is reconstructed the same way the
// tracker used to derive it: the newest unprocessed transaction if any are pending,
// otherwise the last processed one.
func (t *EventTracker) backfillLatestQueriedTransaction() error {
	latestQueried, err := t.storage.GetLatestQueriedTransaction()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get latest queried transaction: %s", err.Error()))

		return err
	}

	if latestQueried.TxSignature != (solana.Signature{}) {
		return nil
	}

	unprocessedTxPoints, err := t.storage.GetAllUnprocessedTransactions()
	if err != nil {
		t.logger.Warn(fmt.Sprintf("Failed to get all unprocessed transactions: %s", err.Error()))

		return err
	}

	if len(unprocessedTxPoints) > 0 {
		latestQueried = unprocessedTxPoints[len(unprocessedTxPoints)-1]
	} else {
		latestQueried, err = t.storage.GetLastProcessedTransaction()
		if err != nil {
			t.logger.Warn(fmt.Sprintf("Failed to get last processed transaction: %s", err.Error()))

			return err
		}
	}

	// nothing to backfill, this is a first run
	if latestQueried.TxSignature == (solana.Signature{}) {
		return nil
	}

	t.logger.Info("Backfilling latest queried transaction",
		"tx signature", latestQueried.TxSignature.String(),
		"slot", latestQueried.Slot)

	if err := t.storage.StoreLatestQueriedTransaction(latestQueried); err != nil {
		return fmt.Errorf("failed to backfill latest queried transaction: %w", err)
	}

	return nil
}

// Fetches tx and does processing
func (t *EventTracker) fetchNextGetFullTxBySignature(ctx context.Context) error {
	txPoint, err := t.getLastUnprocessedTxSignature()
	if err != nil {
		return err
	}

	lastProcessed, err := t.storage.GetLastProcessedTransaction()
	if err != nil {
		return err
	}

	if txPoint.TxSignature == (solana.Signature{}) || txPoint.TxSignature == lastProcessed.TxSignature {
		return nil
	}

	txSignature := txPoint.TxSignature

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

func (t *EventTracker) fetchNextGetSignaturesForAddress(
	ctx context.Context, programID solana.PublicKey, lastQueried store.TxPoint,
) error {
	t.logger.Debug("Fetching new transactions",
		"last queried tx signature", lastQueried.TxSignature.String(),
		"last queried slot", lastQueried.Slot)

	latestFinalizedBlockNumber, err := t.client.GetBlockHeight(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return err
	}

	// Query until the last processed signature
	txSignatures, err := t.signaturesForAddressFetcher(ctx, programID, lastQueried)
	if err != nil {
		return err
	}

	// Filter out potential duplicates
	queuedTxSignatures, err := t.getQueuedTxSignatures()
	if err != nil {
		return fmt.Errorf("failed to get queued tx signatures: %w", err)
	}

	txSignatures = slices.DeleteFunc(txSignatures, func(txSig *rpc.TransactionSignature) bool {
		if _, exists := queuedTxSignatures[txSig.Signature]; !exists {
			return false
		}

		t.logger.Debug("Skipping duplicate transaction signature", "tx signature", txSig.Signature.String())

		return true
	})

	if len(txSignatures) == 0 {
		t.logger.Debug("No new transactions found, setting new finalized block number", "number", latestFinalizedBlockNumber)

		err = t.storage.StoreLatestFinalizedBlockNumber(latestFinalizedBlockNumber)
		if err != nil {
			return fmt.Errorf("failed to store latest finalized block number: %w", err)
		}

		return nil
	}

	t.logger.Debug("Fetched new transactions", "count",
		len(txSignatures), "last queried tx signature", txSignatures[0].Signature.String())

	// Since signatures are queried in reverse order, we need to store them in
	// reverse order to get the correct order for processing
	unprocessedTxPoints := make([]store.TxPoint, 0, len(txSignatures))

	for i := len(txSignatures) - 1; i >= 0; i-- {
		// If the last queried tx signature is empty, we need to check if the tx slot is greater than the chain head slot
		// if it is, we skip the tx
		// important for the first run
		if lastQueried.TxSignature == (solana.Signature{}) {
			if t.startFromSlot > txSignatures[i].Slot {
				continue
			}
		}

		unprocessedTxPoints = append(unprocessedTxPoints, store.TxPoint{
			TxSignature: txSignatures[i].Signature,
			Slot:        txSignatures[i].Slot,
		})
	}

	// If we skipped all the txs, we need to set the last queried tx signature to the newest one
	// and set the last processed transaction to the latest one
	if len(unprocessedTxPoints) == 0 {
		newestTxPoint := store.TxPoint{
			TxSignature: txSignatures[0].Signature,
			Slot:        txSignatures[0].Slot,
		}

		err = t.storage.SetLastProcessedTransaction(newestTxPoint)
		if err != nil {
			return fmt.Errorf("failed to set last processed transaction on start: %w", err)
		}

		err = t.storage.StoreLatestQueriedTransaction(newestTxPoint)
		if err != nil {
			return fmt.Errorf("failed to store latest queried transaction on start: %w", err)
		}

		return nil
	}

	err = t.storage.PushUnprocessedTransactions(unprocessedTxPoints)
	if err != nil {
		return fmt.Errorf("failed to push unprocessed transactions: %w", err)
	}

	err = t.storage.StoreLatestQueriedTransaction(unprocessedTxPoints[len(unprocessedTxPoints)-1])
	if err != nil {
		return fmt.Errorf("failed to store latest queried transaction: %w", err)
	}

	return nil
}

func (t *EventTracker) signaturesForAddressFetcher(
	ctx context.Context,
	programID solana.PublicKey,
	lastQueried store.TxPoint,
) ([]*rpc.TransactionSignature, error) {
	cursorNotFound := false
	// 1. Fetch signatures for address
	txSignatures, err := t.getSignaturesForAddressHelper(ctx, programID, nil, &lastQueried.TxSignature)
	if err != nil {
		// Unrecoverable error, return it
		if !IsCursorNotFoundErr(err) {
			return nil, err
		}

		// Our last queried sig is no longer valid, we need to fetch by slot instead
		t.logger.Warn(fmt.Sprintf("Last queried signature %s not found, fetching by slot instead",
			lastQueried.TxSignature.String()))

		cursorNotFound = true

		// 2. Fetch signatures for address by slot
		txSignatures, err = t.getSignaturesForAddressHelper(ctx, programID, nil, nil)
		if err != nil {
			return nil, err
		}

		signaturesToKeep := make([]*rpc.TransactionSignature, 0, len(txSignatures))

		for _, txSig := range txSignatures {
			// We check for >= since there could be more than 1 tx in the same slot, and we want to keep all of them
			if txSig.Slot >= lastQueried.Slot {
				signaturesToKeep = append(signaturesToKeep, txSig)
			}
		}

		txSignatures = signaturesToKeep
	}

	if len(txSignatures) == getSignaturesForAddressMaxLimit {
		// We have to fetch what was before the last processed signature
		// since the initial query returned the max limit of signatures
		signatures, err := t.catchUpLoop(
			ctx, programID, lastQueried, txSignatures[len(txSignatures)-1].Signature, cursorNotFound)
		if err != nil {
			return nil, err
		}

		txSignatures = append(txSignatures, signatures...)
	}

	return txSignatures, err
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

func (t *EventTracker) catchUpLoop(
	ctx context.Context,
	programID solana.PublicKey,
	lastQueried store.TxPoint,
	beforeStart solana.Signature,
	cursorNotFound bool,
) (signatures []*rpc.TransactionSignature, err error) {
	t.logger.Debug("Catching up loop", "last processed tx signature", beforeStart.String())

	before := beforeStart
	until := lastQueried.TxSignature

	// In case that the last queried signature is not found, we need to fetch until the beginning

	if cursorNotFound {
		until = solana.Signature{}
	}

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

		if cursorNotFound {
			for _, txSig := range txSignatures {
				// We check for >= since there could be more than 1 tx in the same slot, and we want to keep all of them
				if txSig.Slot >= lastQueried.Slot {
					signatures = append(signatures, txSig)
				}
			}
		} else {
			signatures = append(signatures, txSignatures...)
		}

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
		queued[txSignature.TxSignature] = struct{}{}
	}

	if lastProcessedTxSignature.TxSignature != (solana.Signature{}) {
		queued[lastProcessedTxSignature.TxSignature] = struct{}{}
	}

	return queued, nil
}
