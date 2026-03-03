package tracker

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Ethernal-Tech/solana-infrastructure/tracker/store"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/hashicorp/go-hclog"

	binary "github.com/gagliardetto/binary"
)

type eventTrackerState string

var (
	active     eventTrackerState = "active"
	paused     eventTrackerState = "paused"
	inactive   eventTrackerState = "inactive"
	terminated eventTrackerState = "terminated"
)

// SlotNotification represents a notification sent on the chSlot channel.
type SlotNotification struct {
	// SlotNumber is the number of the slot that was just processed.
	SlotNumber uint64

	// BlockExists indicates whether a block actually exists in this slot. True if a block exists,
	// false if the slot is empty.
	BlockExists bool
}

// EventNotification represents a notification sent on the chEvent channel.
type EventNotification struct {
	// SlotNumber is the number of the slot in which the tracked event was emitted.
	SlotNumber uint64

	// Program is the public key (address) of the Solana program that emitted the tracked event.
	Program solana.PublicKey

	// EventName is the name of the event, as registered in the corresponding ProgramEventSpecs.
	EventName string

	// EventData is the deserialized event payload. The dynamic type of the returned value is a
	// pointer, and the pointed data MUST be treated as read-only. Modifying it may result in a
	// data race. In order to modify it, create a deep copy.
	EventData any
}

// ErrorNotification represents a notification sent on the chError channel.
type ErrorNotification struct {
	// Error is the concrete error that occurred during event tracker execution.
	error

	// Terminated indicates whether this error will cause the event tracker to terminate. If
	// true, the tracker entered graceful termination process. A true value has the same effect
	// as calling the [Terminate] method, so all described in its documentation apply.
	Terminated bool
}

// NotificationConfig holds channel buffer sizes for slot, event, and error
// notifications. If zero is provided for any channel, that channel will be unbuffered.
type NotificationConfig struct {
	SlotBuffSize  uint8
	EventBuffSize uint8
	ErrorBuffSize uint8
}

// EventTrackerConfig holds the configuration for an EventTracker.
type EventTrackerConfig struct {
	// RPCEndpoint is the full Solana JSON-RPC URL.
	// Used only if Client is nil — in that case a new rpc.Client is created
	// from this endpoint and stored back into Client.
	RPCEndpoint string `json:"rpcEndpoint"`

	// Client is the Solana RPC client. If nil, RPCEndpoint must be set and
	// a client will be constructed automatically.
	Client *rpc.Client `json:"-"`

	// TrackedPrograms maps each program public key to its event specifications.
	// Must contain at least one entry.
	TrackedPrograms map[string]ProgramEventSpecs `json:"-"`

	// Commitment specifies the confirmation level required before a slot is
	// considered for indexing. Recommended: rpc.CommitmentFinalized.
	Commitment string `json:"commitment"`

	// Logger records state changes and actions during the tracker's lifecycle.
	// If nil, no logging is performed.
	Logger hclog.Logger `json:"-"`

	// EventSink is an optional output where the tracker writes each successfully
	// indexed event (only if the event type implements String(uint64, solana.PublicKey) string).
	// If nil, no events are written.
	EventSink io.Writer `json:"-"`

	// PollTime is the polling interval for checking new blocks/slots. Must be
	// between 200ms and 15 minutes. Zero means 500ms.
	PollTime time.Duration `json:"-"`

	// BlockFetchDelay is the delay between block fetches for rate limiting.
	// Must be between 0 and 30s. Zero means 250ms. Recommended: 250ms for
	// public RPCs, 0 for private RPCs.
	BlockFetchDelay time.Duration `json:"-"`

	// Notifications enables slot, event, and error notification channels.
	// If nil, notifications are disabled.
	Notifications *NotificationConfig `json:"-"`
}

// EventTracker monitors the Solana blockchain for specific program events, emits notifications
// on a channels, and persists data to a storage.
type EventTracker struct {
	// client is the Solana RPC client used to fetch blocks from the blockchain network.
	client *rpc.Client

	// storage is responsible for persisting indexed data. For details, see the [StorageHandler]
	// interface documentation.
	storage store.StorageHandler

	// trackedPrograms defines which programs and events the tracker observes. Only events listed
	// in ProgramEventSpecs for programs present in this map are indexed.
	trackedPrograms map[solana.PublicKey]ProgramEventSpecs

	// commitment specifies the commitment level required for a block/slot to be considered for
	// indexing. Common values are "confirmed" and "finalized" (no chain reorganization). Other
	// levels should not be considered.
	commitment rpc.CommitmentType

	// Optional fields (from [EventTrackerConfig]):

	// logger records state changes and actions during the tracker's lifecycle. By default, no
	// logging is performed.
	logger hclog.Logger

	// eventSink is an optional output destination where the tracker writes event each time an
	// event is successfully indexed and processed. Write will be performed only if the golang
	// type representing the event implements `String(uint64, solana.PublicKey) string` method,
	// where the first argument is the slot number in which the event occurred, and the second
	// is the public key of the program that emitted it. By default, no output
	eventSink io.Writer

	// pollTime specifies the polling interval for checking new blocks/slots. By default, 500
	// milliseconds.
	pollTime time.Duration

	// notifications indicates whether the tracker should send notifications on chSlot, chEvent
	// and chError channels. When false, channels remain nil. When true, channels are created
	// with buffer sizes from config.Notifications.
	notifications bool

	// Internal fields (not settable through [NewEventTracker]):

	// state represents the current status of the tracker, e.g., active, inactive, or paused.
	state eventTrackerState

	// chEvent is a channel used to emit notification each time a tracked event is processed.
	chEvent chan EventNotification

	// chSlot is a channel used to emit notification each time a slot is processed.
	chSlot chan SlotNotification

	// chError is a channel used to emit notifications when an error occurs during event tracker
	// execution.
	chError chan ErrorNotification

	// chPause is a channel used to pause the tracker after completing the current slot.
	chPause chan struct{}

	// chTerminate is a channel used to terminate the tracker gracefully after processing
	// the current slot. Upon termination, chEvent and chSlot channels are closed.
	chTerminate chan struct{}

	// applyTx indicates whether operations on the storage are executed within a transaction. In
	// other words, whether the [(StorageHandler).ApplyTransaction] is invoked. It is set to the
	// return value of the [(StorageHandler).UseTransaction] method.
	applyTx bool

	mut sync.Mutex

	// delay between block fetches for rate limiting
	blockFetchDelay time.Duration
}

// NewEventTracker constructs a new EventTracker instance.
//
// The config argument is required and must not be nil. If config.Client is
// nil, config.RPCEndpoint is used to create one automatically.
//
// The storage argument is required and must not be nil. It is
// passed explicitly so callers can inject any StorageHandler implementation
// (e.g. for testing).
//
// Optional settings (Logger, EventSink, PollTime, BlockFetchDelay, Notifications)
// are taken from config; zero values use defaults (e.g. PollTime 0 → 500ms).
func NewEventTracker(config *EventTrackerConfig, storage store.StorageHandler) (*EventTracker, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if storage == nil {
		return nil, fmt.Errorf("storage cannot be nil")
	}
	if len(config.TrackedPrograms) == 0 {
		return nil, fmt.Errorf("must track at least one program")
	}
	// Set up RPC client if not provided externally
	if err := setupClient(config); err != nil {
		return nil, err
	}

	pollTime := config.PollTime
	if pollTime == 0 {
		pollTime = 500 * time.Millisecond
	}

	blockFetchDelay := config.BlockFetchDelay
	if blockFetchDelay == 0 {
		blockFetchDelay = 250 * time.Millisecond
	}

	if config.Logger == nil {
		config.Logger = hclog.NewNullLogger().Named("event-tracker")
	}

	trackedPrograms := make(map[solana.PublicKey]ProgramEventSpecs, len(config.TrackedPrograms))

	for programIDstr, eventSpecs := range config.TrackedPrograms {
		programID, err := solana.PublicKeyFromBase58(programIDstr)
		if err != nil {
			return nil, fmt.Errorf("invalid program ID %s: %w", programIDstr, err)
		}

		trackedPrograms[programID] = eventSpecs
	}

	commitment := rpc.CommitmentFinalized
	if config.Commitment == "confirmed" {
		commitment = rpc.CommitmentConfirmed
	}

	t := &EventTracker{
		client:          config.Client,
		storage:         storage,
		trackedPrograms: trackedPrograms,
		commitment:      commitment,
		logger:          config.Logger,
		eventSink:       config.EventSink,
		pollTime:        pollTime,
		state:           inactive,
		chPause:         make(chan struct{}),
		chTerminate:     make(chan struct{}),
		blockFetchDelay: blockFetchDelay,
	}

	if config.Notifications != nil {
		t.notifications = true
		n := config.Notifications
		t.chSlot = make(chan SlotNotification, n.SlotBuffSize)
		t.chEvent = make(chan EventNotification, n.EventBuffSize)
		t.chError = make(chan ErrorNotification, n.ErrorBuffSize)
	}

	return t, nil
}

// ChSlot returns a read-only channel that emits a notification each time a slot is successfully
// processed. A notification is emitted even if no block exists for that slot. If notifications
// are not enabled (config.Notifications was nil), a nil channel is returned.
func (t *EventTracker) ChSlot() <-chan SlotNotification {
	return (<-chan SlotNotification)(t.chSlot)
}

// ChEvent returns a read-only channel that emits a notification each time a tracked event is
// successfully processed. If notifications are not enabled (config.Notifications was nil), a nil
// channel is returned.
func (t *EventTracker) ChEvent() <-chan EventNotification {
	return (<-chan EventNotification)(t.chEvent)
}

// ChError returns a read-only channel that emits a notification each time an error occurs during
// event tracker execution. If notifications are not enabled (config.Notifications was nil), a nil
// channel is returned.
func (t *EventTracker) ChError() <-chan ErrorNotification {
	return (<-chan ErrorNotification)(t.chError)
}

// State returns the current state of the EventTracker.
func (t *EventTracker) State() eventTrackerState {
	t.mut.Lock()
	defer t.mut.Unlock()
	return t.state
}

// Pause pauses the running event tracker. If the method returns true, it means the tracker has
// entered the pausing process. Returning from the method does not mean the tracker is paused.
// Use the [State] method to check if it has actually reached the "paused" state. Calling [Start],
// [Terminate], or another [Pause] method before [State] returns "paused" is considered undefined
// behavior. Returning false means the event tracker cannot be paused because it is not in the
// active state.
func (t *EventTracker) Pause() bool {
	if t.State() != active {
		return false
	}

	t.chPause <- struct{}{}

	return true
}

// Terminate gracefully terminates the running event tracker. If the method returns true, it
// means the event tracker has entered the termination process. Returning from the method does
// not mean the tracker is terminated. Use the [State] method to check if it has actually reached
// the "inactive" state. Calling [Start], [Pause], or another [Terminate] method before [State]
// returns "inactive" is considered undefined behavior. Returning false means the event tracker
// cannot be terminated because it is not in the active state.
func (t *EventTracker) Terminate() bool {
	if t.State() != active {
		return false
	}

	t.chTerminate <- struct{}{}

	return true
}

func (t *EventTracker) setState(state eventTrackerState) {
	t.mut.Lock()
	defer t.mut.Unlock()
	t.state = state
}

func sendNotification[T any](ch chan<- T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func (t *EventTracker) notify(notification any) {
	if t.notifications {
		switch value := notification.(type) {
		case SlotNotification:
			sendNotification(t.chSlot, value)
		case EventNotification:
			sendNotification(t.chEvent, value)
		case ErrorNotification:
			sendNotification(t.chError, value)
		}
	}
}

func (t *EventTracker) terminate() {
	if t.notifications {
		close(t.chEvent)
		close(t.chSlot)
		close(t.chError)
	}

	t.storage = nil
	t.setState(terminated)
	t.logger.Info("Event tracker has been terminated")
}

// Start launches the event tracker in a background goroutine, transitioning from inactive to
// active state. The tracker processes blocks continuously until Terminate() or Pause() is called.
// Use Pause() to temporarily halt and Start() to resume processing.
// Once terminated, the tracker cannot be restarted.
func (t *EventTracker) Start() {
	handleError := func(err error, msg string) error {
		t.logger.Error(msg, "err", err)

		return err
	}

	if t.client == nil {
		handleError(nil,
			"method must be invoked on an instance initialized through [NewEventTracker]")
	} else if t.storage == nil {
		handleError(nil,
			"this event tracker instance is terminated, only paused tracker can be again started")
	}
	// prevent starting if already active
	if t.State() == active {
		handleError(nil, "tracker is already running")
	}

	currentSlot, err := t.storage.ReadSlot()
	if err != nil {
		handleError(err, "cannot read starting slot")
	}

	t.applyTx = t.storage.UseTransactions()
	// Transition to active state before starting the goroutine
	t.setState(active)
	go func() {
		t.logger.Info("Starting indexing from slot %d", currentSlot)

		// Polling loop
		for {
			select { // before fetching new slot, check for pause/terminate signals
			case <-t.chPause:
				t.setState(paused)
				t.logger.Info("Event tracker has been paused")
				return // exit goroutine
			case <-t.chTerminate:
				t.terminate()
				return // exit goroutine
			default: // continue with normal execution
			}

			t.logger.Debug("Checking chain head at slot %d", currentSlot)

			fetchedSlot, err := t.client.GetSlot(context.TODO(), t.commitment)
			if err != nil {
				t.notify(
					ErrorNotification{fmt.Errorf("failed to fetch slot %d: %w", currentSlot, err), false})
				t.logger.Error("Failed to fetch slot %d: %s", currentSlot, err.Error())
				t.logger.Info("I will try again in %d ms...", t.pollTime.Milliseconds())
				time.Sleep(t.pollTime)
				continue
			}

			if currentSlot > uint64(fetchedSlot) {
				t.logger.Debug(
					"Reached chain head, waiting for slot %d to be %s (currently last %s: %d)",
					currentSlot,
					t.commitment,
					t.commitment,
					fetchedSlot,
				)
				t.logger.Info("I will try again in %d ms...", t.pollTime.Milliseconds())
				time.Sleep(t.pollTime)
				continue
			}

			// Catch-up loop: Process all available slots up to chain head
			for currentSlot <= uint64(fetchedSlot) {
				// Check for pause/terminate signals during catch-up
				select {
				case <-t.chPause:
					t.setState(paused)
					t.logger.Info("Event tracker has been paused")

					return
				case <-t.chTerminate:
					t.terminate()
					return
				default:
				}
				// Process current slot - cannot interrupt (pause, terminate) during processing of a slot
				t.logger.Info("Slot %d is %s, processing...", currentSlot, t.commitment)

				block, err := t.client.GetBlockWithOpts(context.TODO(), currentSlot, &rpc.GetBlockOpts{
					TransactionDetails:             rpc.TransactionDetailsFull,
					MaxSupportedTransactionVersion: new(uint64),
				})

				if err != nil {
					// Check if slot was skipped
					if isSkippedSlotError(err) {
						if err := t.storage.StoreSlot(nil, currentSlot); err != nil {
							t.notify(ErrorNotification{
								fmt.Errorf("failed to store skipped slot: %w", err), true})
							t.logger.Error("Failed to store skipped slot: %s", err.Error())
							t.terminate()
							return
						}

						t.notify(SlotNotification{currentSlot, false})
						t.logger.Info("Slot %d was skipped (no block produced), moving to next slot", currentSlot)

						currentSlot++
						continue
					}

					// Retry for other errors - break inner loop to re-fetch chain head
					t.notify(ErrorNotification{
						fmt.Errorf("failed to fetch block for slot %d: %w", currentSlot, err), false})
					t.logger.Error("Failed to fetch block for slot %d: %s", currentSlot, err.Error())
					t.logger.Info("I will try again in %d ms...", t.pollTime.Milliseconds())
					time.Sleep(t.pollTime)
					break // Break inner loop, outer loop will retry
				}

				if block == nil {
					if err := t.storage.StoreSlot(nil, currentSlot); err != nil {
						t.notify(ErrorNotification{
							fmt.Errorf("failed to store slot: %w", err), true})
						t.logger.Error("Failed to store slot: %s", err.Error())
						t.terminate()
						return
					}

					t.notify(SlotNotification{currentSlot, false})
					t.logger.Debug("Slot %d is empty", currentSlot)

					currentSlot++
					continue
				}

				t.logger.Info("Block in slot %d has %d transactions", currentSlot, len(block.Transactions))

				if !t.processBlock(currentSlot, block) {
					return
				}

				t.notify(SlotNotification{currentSlot, true})

				currentSlot++
				if t.blockFetchDelay > 0 {
					time.Sleep(t.blockFetchDelay) // Rate limiting between block fetches to avoid hitting RPC limits during catch-up
				}
			}

			if currentSlot > uint64(fetchedSlot) {
				// Sleep when caught up to chain head
				t.logger.Debug("Processed up to slot %d, waiting for new blocks...", currentSlot-1)
				time.Sleep(t.pollTime)
			}
		}
	}()

}

func (t *EventTracker) processBlock(slot uint64, block *rpc.GetBlockResult) bool {
	// TODO: We should also check whether any of the tracked programs was called via a CPI.

	var eventFns []func(st store.StorageTransaction) error
	// Store event details for post-commit notifications in transaction mode
	var pendingNotifications []EventNotification

	trackedInTx := make(map[solana.PublicKey]bool)
	for txIndex, tx := range block.Transactions {
		transaction, err := tx.GetTransaction()
		if err != nil {
			t.notify(ErrorNotification{
				fmt.Errorf("failed to decode transaction %d: %s", txIndex+1, err), false})

			t.logger.Warn("Failed to decode transaction %d: %s", txIndex+1, err.Error())

			continue
		}

		if tx.Meta == nil {
			t.notify(ErrorNotification{
				fmt.Errorf("cannot read meta data for the transaction"), false})

			t.logger.Warn("Cannot read meta data for the transaction")

			continue
		}

		if len(tx.Meta.LogMessages) == 0 {
			continue
		}

		for _, instruction := range transaction.Message.Instructions {
			if int(instruction.ProgramIDIndex) >= len(transaction.Message.AccountKeys) {
				t.logger.Warn("Invalid ProgramIDIndex %d in transaction %d (only %d accounts)",
					instruction.ProgramIDIndex, txIndex+1, len(transaction.Message.AccountKeys))
				continue
			}
			programID := transaction.Message.AccountKeys[instruction.ProgramIDIndex]

			if _, ok := t.trackedPrograms[programID]; ok {
				trackedInTx[programID] = true
			}

			if _, ok := t.trackedPrograms[programID]; !ok {
				continue
			}

			for _, log := range tx.Meta.LogMessages {
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
					t.notify(ErrorNotification{
						fmt.Errorf("failed to decode log: %w", err), false})

					t.logger.Warn("Failed to decode log: %s", err.Error())

					continue
				}

				// Try to parse with each tracked program in this transaction
				for programID := range trackedInTx {
					parsed, name, err := t.parseEvent(decoded, programID)
					if err != nil {
						t.notify(ErrorNotification{
							fmt.Errorf("failed to parse event: %w", err), false})

						t.logger.Warn("Failed to parse event: %s", err.Error())

						continue
					}

					if parsed == nil {
						continue
					}

					// Found a matching event!
					if t.applyTx {
						pendingNotifications = append(pendingNotifications, EventNotification{
							SlotNumber: slot,
							Program:    programID,
							EventName:  name,
							EventData:  parsed,
						})

						eventFns = append(eventFns, func(st store.StorageTransaction) error {
							return t.storage.StoreEvent(
								st,
								slot,
								programID,
								name,
								parsed,
							)
						})
					} else {
						if err := t.storage.StoreEvent(nil, slot, programID, name, parsed); err != nil {
							t.notify(ErrorNotification{
								fmt.Errorf("failed to store event: %w", err), true})

							t.logger.Error("Failed to store event: %s", err.Error())

							t.terminate()

							return false
						}

						t.notify(EventNotification{
							SlotNumber: slot,
							Program:    programID,
							EventName:  name,
							EventData:  parsed,
						})

						t.logger.Info(fmt.Sprintf("Event of type %s emitted by %s at slot %d", name, programID, slot))
					}

					if str, ok := parsed.(interface {
						String(uint64, solana.PublicKey) string
					}); t.eventSink != nil && ok {
						if _, err := t.eventSink.Write([]byte(str.String(slot, programID))); err != nil {
							t.notify(ErrorNotification{
								fmt.Errorf("failed to write in event sink: %w", err), false})

							t.logger.Warn("Failed to write in event sink: %s", err.Error())
						}
					}

					break // Found the correct program, no need to try others
				}
			}
		}
	}

	if t.applyTx {
		slotFn := func(st store.StorageTransaction) error {
			return t.storage.StoreSlot(
				st,
				slot,
			)
		}

		if err := t.storage.ApplyTransaction(slotFn, eventFns); err != nil {
			t.notify(ErrorNotification{
				fmt.Errorf("failed to apply storage transaction: %w", err), true})

			t.logger.Error("Failed to apply storage transaction: %s", err.Error())

			t.terminate()

			return false
		}

		// Send notifications AFTER successful transaction commit
		for _, notification := range pendingNotifications {
			t.notify(notification)
			t.logger.Info(fmt.Sprintf("Event of type %s emitted by %s at slot %d",
				notification.EventName, notification.Program, notification.SlotNumber))
		}

		t.logger.Debug("Successfully stored %d events for slot %d", len(eventFns), slot)

		return true
	}

	if err := t.storage.StoreSlot(nil, slot); err != nil {
		t.notify(ErrorNotification{
			fmt.Errorf("failed to store slot: %w", err), true})

		t.logger.Error("Failed to store slot: %s", err.Error())

		t.terminate()

		return false
	}

	return true
}

func (t *EventTracker) parseEvent(eventData []byte, programID solana.PublicKey) (any, string, error) {
	decoder := binary.NewBorshDecoder(eventData)
	discriminator, err := decoder.ReadDiscriminator()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get event discriminator: %w", err)
	}

	trackedEvents, ok := t.trackedPrograms[programID] // checked existence of programID in processBlock
	if !ok {
		t.logger.Warn("Program %s not found in tracked programs", programID)
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

// helper that checks if the error indicates a skipped/missing slot
func isSkippedSlotError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "-32007") ||
		strings.Contains(errStr, "was skipped") ||
		strings.Contains(errStr, "missing due to ledger jump")
}

// setupClient initialises config.Client if it is not already set.
func setupClient(config *EventTrackerConfig) error {
	if config.Client != nil {
		return nil
	}
	if config.RPCEndpoint == "" {
		return fmt.Errorf("either config.Client or config.RPCEndpoint must be set")
	}
	config.Client = rpc.New(config.RPCEndpoint)
	return nil
}
