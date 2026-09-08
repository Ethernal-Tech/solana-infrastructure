package tracker_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Ethernal-Tech/solana-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/tracker"
	"github.com/Ethernal-Tech/solana-infrastructure/tracker/internal/trackertest"
	"github.com/Ethernal-Tech/solana-infrastructure/tracker/store"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

const (
	// simulationDuration is how long the tracker runs against the live simulated chain.
	simulationDuration = time.Minute

	// catchUpSimulationDuration is the live phase of the catch-up test. What it verifies
	// happens at startup, so it only has to run long enough to also see live events.
	catchUpSimulationDuration = 20 * time.Second

	// catchUpHistoryTxCount is the size of the pre-existing history for the catch-up
	// tests. getSignaturesForAddress answers at most 1000 signatures per call, so this
	// forces the tracker through extra pages before it reaches the end.
	catchUpHistoryTxCount = 2500

	// wholeHistoryTxCount is the history size for the whole-history test, which has to
	// process every one of these transactions rather than skip them. It still exceeds a
	// single signature page, so the catch-up paging is exercised too.
	wholeHistoryTxCount = 1200

	// wholeHistoryTimeout bounds how long the whole-history test waits for the entire
	// history to be delivered before giving up.
	wholeHistoryTimeout = 3 * time.Minute

	// The restart test alternates between running and being down. Each live phase has to
	// be long enough to see several transactions, and the downtime long enough for the
	// chain to build a backlog the restarted tracker then has to pick up.
	restartLivePhase = 15 * time.Second
	restartDowntime  = 10 * time.Second

	// restartCatchUpTimeout bounds how long the restarted tracker gets to work through the
	// backlog that built up while it was down.
	restartCatchUpTimeout = time.Minute

	// outageGapSlots is the length of the empty run the outage test injects. It has to
	// exceed chainHeadWindowSlots, so that a whole getBlocks query lands inside the gap and
	// comes back with nothing for the chain head to advance to.
	outageGapSlots = chainHeadWindowSlots + 30

	// The sparse stretch scripted by the outage test. It starts sparseRegionStartOffset slots
	// into the chain, covers sparseRegionSlots slots, and holds sparseRegionBlockCount blocks,
	// all bunched at its start. The stretch is as wide as the queried window, so a window
	// opened on the last block in it still cannot see the chain resume, and the empty part of
	// it is shorter than the window, so every query still comes back with blocks in it.
	sparseRegionStartOffset = 100
	sparseRegionSlots       = chainHeadWindowSlots
	sparseRegionBlockCount  = 5

	// sparseRegionThresholdBlocks is how many blocks the other shape of the stretch holds, the
	// one that lands them on the slots the tracker rounds to: one per rounding threshold.
	sparseRegionThresholdBlocks = sparseRegionSlots / defaultBlockRoundingThreshold

	// sparseRegionTailSlots is how much chain follows the stretch before the tracker starts, so
	// the whole stretch is already history by the time the tracker meets it.
	sparseRegionTailSlots = 50

	// outageSettleSlots is how far past the outage the chain must get before the tracker is
	// expected to have anything on the other side to find.
	outageSettleSlots = 5

	// outageLivePhase is how long the chain runs normally either side of the outage, and
	// outageTimeout bounds each wait for the chain or the tracker to get past it.
	// The outage itself costs outageGapSlots of mainnet slot time to play out, so the waits
	// either side of it have to be able to outlast that.
	outageLivePhase = 8 * time.Second
	outageTimeout   = time.Minute

	// maxForceAdvances is how many force advances crossing the outage may reasonably take:
	// one per forced step over it, with room to spare.
	maxForceAdvances = 30

	// The forgotten-cursor test needs the program to go quiet long enough that the query
	// cursor is clearly parked, then to spend a stretch with every query answered by the
	// not-found error before transactions start landing again.
	cursorQuietPhase = 15 * time.Second
	cursorErrorPhase = 30 * time.Second

	// cursorWarmUpEvents is how many events the tracker has to deliver before the program goes
	// quiet, so there is a real cursor to lose, and cursorTimeout bounds each wait for it.
	cursorWarmUpEvents = 3
	cursorTimeout      = time.Minute

	// cursorSettlePhase lets a recovered tracker run on for a while before the run is judged.
	cursorSettlePhase = 5 * time.Second

	// skipAllHeadroomSlots is how many transaction-free slots the skip-everything test puts
	// between the newest transaction on the chain and the slot the tracker starts from.
	skipAllHeadroomSlots = 5

	// sameSlotTxCount is how many transactions the duplicate-filter test lands in one slot:
	// one still queued and the rest already processed.
	sameSlotTxCount = 3

	// catchUpBurstTxCount is the backlog waiting behind a forgotten cursor. It has to exceed
	// signaturesPerPage so the cursorless fallback query comes back full and the tracker has
	// to page back through the remainder.
	catchUpBurstTxCount = 1500

	// catchUpBurstTimeout bounds how long the tracker gets to work through that backlog.
	catchUpBurstTimeout = 2 * time.Minute

	// signaturesPerPage is the validator's getSignaturesForAddress page size.
	signaturesPerPage = 1000

	// deliveryLagAllowance is how far behind the chain head the tracker is allowed to be
	// at shutdown. Events produced earlier than this before the stop must all have been
	// delivered, which is what turns these tests into stall detectors.
	deliveryLagAllowance = 15 * time.Second

	// chainHeadWindowSlots is the size of the slot window the tracker asks getBlocks for. It
	// has to match the tracker's chain head slot offset, because the histories scripted below
	// are shaped against it: what stalls a chain head is a stretch the window cannot see out
	// of, so widening the window without widening those stretches stops them stalling anything.
	// chainHeadWindowSlack is how many calls per window a healthy refresher is allowed before
	// the count counts as spinning.
	chainHeadWindowSlots = 80
	chainHeadWindowSlack = 10

	// defaultBlockRoundingThreshold is the BlockRoundingThreshold the runs configure, i.e. the
	// slot boundary the tracker rounds to when picking which blocks to fetch.
	defaultBlockRoundingThreshold = 10

	// chainHeadLagAllowanceSlots bounds how far the stored chain head may trail the
	// simulated head. The tracker advances the head in blockRoundingThreshold-sized
	// steps and idles when it is close to the tip, so some lag is expected.
	chainHeadLagAllowanceSlots = 60

	trackedEventName = "HotWalletIncrementEvent"
)

// TestEventTracker_HappyPath_SimulatedChain runs the tracker for a full minute against a
// simulated chain that keeps producing slots, blocks and tracked-program transactions in
// real time, and asserts it delivers every event exactly once, in order, and keeps up with
// the chain head.
func TestEventTracker_HappyPath_SimulatedChain(t *testing.T) {
	sim := trackertest.NewChainSimulator(t, trackertest.DefaultConfig())
	run := runTracker(t, sim, runConfig{
		duration:           simulationDuration,
		startFromChainHead: true,
	})

	requireDeliveredPrefix(t, run)
	requireKeptUpWithChain(t, run)
	requireTxStateConsistent(t, run)
	requireChainHeadAdvanced(t, run)
}

// TestEventTracker_CatchesUpOnStartup starts the tracker against a chain whose history is
// deeper than a single getSignaturesForAddress page, so the first query comes back full and
// the tracker has to page backwards to find the end of the history. It asserts the paging
// actually happened, that it walked the whole history, that it happened only once, and that
// live events are still delivered correctly afterwards.
func TestEventTracker_CatchesUpOnStartup(t *testing.T) {
	cfg := trackertest.DefaultConfig()
	cfg.HistoryTxCount = catchUpHistoryTxCount

	sim := trackertest.NewChainSimulator(t, cfg)
	require.GreaterOrEqual(t, sim.ProducedTxCount(), catchUpHistoryTxCount)

	historyTxCount := sim.ProducedTxCount()

	run := runTracker(t, sim, runConfig{
		duration:           catchUpSimulationDuration,
		startFromChainHead: true,
	})

	requireStartupPagedThroughHistory(t, run, historyTxCount)
	requireDeliveredPrefix(t, run)
	requireKeptUpWithChain(t, run)
	requireTxStateConsistent(t, run)
	requireChainHeadAdvanced(t, run)
}

// TestEventTracker_SkipsAllHistoryOnStartup covers the first run where every signature the node
// answers with is older than the configured start slot, so the tracker skips the lot and has
// nothing to report. It still has to write down where it got to: the newest signature it saw
// becomes both the last processed and the last queried transaction, because without that the
// next query would start from the beginning of the program's history all over again.
func TestEventTracker_SkipsAllHistoryOnStartup(t *testing.T) {
	sim := trackertest.NewChainSimulator(t, trackertest.DefaultConfig())

	history := sim.ProducedTxsFromSlot(sim.GenesisSlot())
	require.NotEmpty(t, history, "there is no history to skip")

	newestSkipped := history[len(history)-1]

	// Nothing new may land while the tracker starts up, otherwise it would have something to
	// report and this is no longer the skip-everything case. The chain keeps producing blocks,
	// so the head moves past the newest transaction and the start slot ends up above all of it.
	sim.PauseTransactions()
	sim.ProduceSlots(skipAllHeadroomSlots)

	run := newSimRun(t, sim, runConfig{startFromChainHead: true})
	require.Greater(t, run.startFromSlot, newestSkipped.Slot,
		"the start slot has to sit above every transaction on the chain for all of them to be skipped")

	run.startTracker()

	// The tracker records where it got to even though it reported nothing.
	require.True(t, run.waitForCondition("the tracker to record the history it skipped", cursorTimeout,
		func() bool {
			latestQueried, err := run.storage.GetLatestQueriedTransaction()

			return err == nil && latestQueried.TxSignature != (solana.Signature{})
		}),
		"the tracker skipped the whole history without recording where it got to, so it would "+
			"query it all again")

	// Both records have to name the newest signature the query answered with, slot included.
	expectedPoint := store.TxPoint{TxSignature: newestSkipped.Signature, Slot: newestSkipped.Slot}

	latestQueried, err := run.storage.GetLatestQueriedTransaction()
	require.NoError(t, err)
	require.Equal(t, expectedPoint, latestQueried, "wrong latest queried transaction recorded")

	lastProcessed, err := run.storage.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, expectedPoint, lastProcessed, "wrong last processed transaction recorded")

	// Skipped means skipped: none of that history may be reported.
	require.Zero(t, run.deliveredCount(), "the tracker reported transactions it was supposed to skip")

	// Nothing is left queued either, so a restart has no phantom backlog to work through.
	unprocessed, err := run.storage.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Empty(t, unprocessed, "the skipped transactions were queued for processing anyway")

	// From here the recorded cursor has to work: transactions landing now are the first ones
	// the tracker owes us, and the skipped history stays skipped.
	sim.ResumeTransactions()
	run.waitForDelivered(1, cursorTimeout)
	time.Sleep(cursorSettlePhase)
	run.stopTracker()

	requireDeliveredPrefix(t, run)
	requireKeptUpWithChain(t, run)
	requireTxStateConsistent(t, run)
	requireChainHeadAdvanced(t, run)

	skipped := make(map[solana.Signature]struct{}, len(history))
	for _, tx := range history {
		skipped[tx.Signature] = struct{}{}
	}

	for _, event := range run.delivered {
		require.NotContains(t, skipped, event.TxSignature,
			"transaction %s was skipped on startup but reported later", event.TxSignature)
	}

	t.Logf("skipped %d transactions up to slot %d, then delivered %d events from slot %d onwards",
		len(history), newestSkipped.Slot, len(run.delivered), run.startFromSlot)
}

// TestEventTracker_IndexesWholeHistory starts the tracker with no StartFromSlot, which is
// the "index everything this program ever did" configuration. Nothing may be skipped: the
// tracker has to page through the whole history and then deliver every transaction in it, in
// production order, before it moves on to live ones.
func TestEventTracker_IndexesWholeHistory(t *testing.T) {
	cfg := trackertest.DefaultConfig()
	cfg.HistoryTxCount = wholeHistoryTxCount
	// Draining the history is bound by how fast the tracker can be answered, not by the
	// chain, so keep the RPC responses immediate.
	cfg.RPCLatency = 0

	sim := trackertest.NewChainSimulator(t, cfg)
	require.GreaterOrEqual(t, sim.ProducedTxCount(), wholeHistoryTxCount)

	historyTxCount := sim.ProducedTxCount()

	run := runTracker(t, sim, runConfig{
		// Start at the first slot of the chain, so nothing in the history is skipped.
		// Leaving startFromSlot unset would index the same transactions, but it also
		// starts the chain head refresher at slot zero, from where it never catches up.
		startFromSlot: sim.GenesisSlot(),
		duration:      wholeHistoryTimeout,
		waitForEvents: historyTxCount,
		rpcLimits:     generousRPCMethodLimits(),
	})

	requireStartupPagedThroughHistory(t, run, historyTxCount)
	requireDeliveredPrefix(t, run)

	// Nothing may be skipped: every transaction the chain had before the tracker started
	// must have been delivered.
	require.GreaterOrEqual(t, len(run.delivered), historyTxCount,
		"tracker delivered %d of the %d transactions in the history", len(run.delivered), historyTxCount)

	// Starting at the first slot of the chain means the head refresher has the whole history
	// to walk as well, so it is still catching up when the backlog is drained. That is why
	// requireChainHeadAdvanced is not called here. It must be walking it in block-sized
	// steps though, not spinning: a refresher that makes no progress force-advances a few
	// slots at a time and re-queries immediately, which shows up as a getBlocks count orders
	// of magnitude above the number of windows the chain actually has.
	windows := (sim.CurrentHeadSlot() - sim.GenesisSlot()) / chainHeadWindowSlots
	require.Less(t, run.sim.RPCCallCounts()["getBlocks"], int(windows)*chainHeadWindowSlack,
		"the chain head refresher queried getBlocks far more often than the chain has windows")

	requireTxStateConsistent(t, run)
}

// TestEventTracker_ResumesAfterRestart stops the tracker while the chain keeps producing and
// starts a fresh one against the same database, twice. Everything the tracker knows about its
// progress has to come back out of storage, so the downtime may only delay events, never drop
// or repeat them.
func TestEventTracker_ResumesAfterRestart(t *testing.T) {
	sim := trackertest.NewChainSimulator(t, trackertest.DefaultConfig())
	run := newSimRun(t, sim, runConfig{startFromChainHead: true})

	// First life: deliver events from a live chain.
	run.startTracker()
	time.Sleep(restartLivePhase)
	run.stopTracker()

	requireDeliveredPrefix(t, run)

	deliveredBeforeRestart := len(run.delivered)
	require.NotEmpty(t, deliveredBeforeRestart, "nothing was delivered before the restart")

	// The chain keeps moving while the tracker is down, so the next life has a backlog.
	downtimeStart := time.Now().UTC()
	time.Sleep(restartDowntime)
	downtimeEnd := time.Now().UTC()

	producedDuringDowntime := txsProducedBetween(run.expectedNow(), downtimeStart, downtimeEnd)
	require.NotEmpty(t, producedDuringDowntime,
		"the chain produced nothing while the tracker was down, so the restart proves nothing")

	// Second life: a fresh tracker on the same database has to pick the backlog up.
	run.startTracker()
	run.waitForDelivered(deliveredBeforeRestart+len(producedDuringDowntime), restartCatchUpTimeout)
	time.Sleep(restartLivePhase)
	run.stopTracker()

	t.Logf("delivered %d events before the restart, %d produced during the %s downtime, %d in total",
		deliveredBeforeRestart, len(producedDuringDowntime), restartDowntime, len(run.delivered))

	// The delivered stream has to read as one uninterrupted sequence across both lives: no
	// gap at the restart boundary, and no event delivered twice because the tracker forgot
	// it had already processed it.
	requireDeliveredPrefix(t, run)
	requireKeptUpWithChain(t, run)
	requireTxStateConsistent(t, run)
	requireChainHeadAdvanced(t, run)

	// Spelled out for the downtime specifically: every transaction the tracker was not
	// running for must have arrived after the restart.
	deliveredSignatures := make(map[solana.Signature]int, len(run.delivered))
	for _, event := range run.delivered {
		deliveredSignatures[event.TxSignature]++
	}

	for _, tx := range producedDuringDowntime {
		require.Equal(t, 1, deliveredSignatures[tx.Signature],
			"transaction %s (slot %d) produced during the downtime was delivered %d times, want exactly once",
			tx.Signature, tx.Slot, deliveredSignatures[tx.Signature])
	}
}

// TestEventTracker_UnsticksChainHeadAfterOutage reproduces the stretch of chain that stopped
// the tracker in production: a long run of full slots, then a stretch holding only a handful
// of blocks all bunched at its start, then full slots again. The empty part of that stretch is
// shorter than the window the tracker queries, so getBlocks keeps answering with blocks and
// the tracker never looks stalled from the outside. What stalls is the head itself: the only
// slot getSlotsToQueryBlocks picks out of that window is the one the head already sits on, so
// there is nothing to advance to, and every following query returns exactly the same window.
//
// The whole stretch is already history by the time the tracker starts, which is how the tracker
// meets it after a restart or when it is catching up.
func TestEventTracker_UnsticksChainHeadAfterOutage(t *testing.T) {
	cfg := trackertest.DefaultConfig()
	// The history is scripted slot by slot below instead of generated, and a slot carries a
	// block unless the script says otherwise.
	cfg.HistorySlots = 0
	cfg.EmptySlotEvery = 0

	sim := trackertest.NewChainSimulator(t, cfg)
	genesis := sim.GenesisSlot()

	// A block in every slot up to the sparse stretch.
	sim.ProduceSlotsWithBlocks(sparseRegionStartOffset)

	// The sparse stretch: five blocks bunched at the start of it, then nothing at all for the
	// rest, which is still short enough to fit inside the queried window.
	sim.ProduceSlotsWithBlocks(2) // +101, +102
	sim.ProduceEmptySlots(1)      // +103
	sim.ProduceSlotsWithBlocks(2) // +104, +105
	sim.ProduceEmptySlots(1)      // +106
	sim.ProduceSlotsWithBlocks(1) // +107
	sim.ProduceEmptySlots(sparseRegionSlots - 7)

	// Full slots again, so by the time the tracker starts the chain is well past the stretch.
	sim.ProduceSlotsWithBlocks(sparseRegionTailSlots)

	sparseRegionStart := genesis + sparseRegionStartOffset
	sparseRegionEnd := sparseRegionStart + sparseRegionSlots

	requireSparseRegionShape(t, sim, sparseRegionStart, sparseRegionEnd)

	// Start at the first slot of the chain, so the head has to walk into the stretch.
	run := newSimRun(t, sim, runConfig{
		startFromSlot: genesis,
		rpcLimits:     generousRPCMethodLimits(),
	})

	run.startTracker()

	// The head has to come out the other side of the stretch.
	recovered := run.waitForCondition("the tracker to advance its chain head past the sparse stretch",
		outageTimeout, func() bool {
			blockPoint, err := run.storage.GetLatestBlockPoint()

			return err == nil && blockPoint != nil && blockPoint.BlockSlot > sparseRegionEnd
		})

	forceAdvances := strings.Count(run.logs.String(), "Chain head stuck, force advancing")

	require.True(t, recovered,
		"the tracker never advanced its chain head past the sparse stretch at slots %d to %d "+
			"(%d force advances)", sparseRegionStart, sparseRegionEnd, forceAdvances)

	// Getting across it needs the force-advance path: the head sits on the only slot the
	// window offers, so nothing else can move it.
	require.NotZero(t, forceAdvances,
		"the tracker crossed the sparse stretch without ever force advancing its chain head")
	require.LessOrEqual(t, forceAdvances, maxForceAdvances,
		"the chain head force advanced %d times crossing the sparse stretch, so it is running "+
			"ahead of the chain instead of catching up with it", forceAdvances)

	// Let it settle into normal operation before looking at the result.
	time.Sleep(outageLivePhase)
	run.stopTracker()

	blockPoint := requireLatestBlockPoint(t, run)
	require.Greater(t, blockPoint.BlockSlot, sparseRegionEnd)
	requireChainHeadAdvanced(t, run)

	// The stretch cost no transactions either, including the ones inside it.
	requireDeliveredPrefix(t, run)
	requireTxStateConsistent(t, run)

	t.Logf("chain head reached slot %d past a sparse stretch at slots %d to %d after %d force advances; "+
		"delivered %d of %d events", blockPoint.BlockSlot, sparseRegionStart, sparseRegionEnd,
		forceAdvances, len(run.delivered), len(run.expected))
}

// TestEventTracker_RecoversFromForgottenQueryCursor reproduces what happened on devnet: the
// program went quiet for a long time, so the tracker's query cursor sat on the same signature
// while the node's history window rolled forward past it. From then on every
// getSignaturesForAddress carrying that cursor was answered with "transaction not found"
// instead of a list, and the tracker had no newer signature to ask with either.
//
// It has to notice the cursor is gone, fall back to querying by slot, and pick transactions up
// again once the program starts producing them, without stalling and without replaying anything
// it already delivered.
func TestEventTracker_RecoversFromForgottenQueryCursor(t *testing.T) {
	sim := trackertest.NewChainSimulator(t, trackertest.DefaultConfig())
	run := newSimRun(t, sim, runConfig{startFromChainHead: true})

	// Normal operation, so the tracker has a cursor to lose.
	run.startTracker()
	run.waitForDelivered(cursorWarmUpEvents, cursorTimeout)

	deliveredBeforeQuiet := run.deliveredCount()
	require.GreaterOrEqual(t, deliveredBeforeQuiet, cursorWarmUpEvents,
		"the tracker never got going, so there is no cursor to lose")

	// The program goes quiet. Blocks keep coming with nothing tracked in them, so the cursor
	// stops moving and the last queried transaction stays the last processed one.
	sim.PauseTransactions()
	time.Sleep(cursorQuietPhase)

	latestQueriedBefore, err := run.storage.GetLatestQueriedTransaction()
	require.NoError(t, err)

	lastProcessed, err := run.storage.GetLastProcessedTransaction()
	require.NoError(t, err)
	require.Equal(t, lastProcessed.TxSignature, latestQueriedBefore.TxSignature,
		"the quiet stretch was supposed to leave the queried and processed cursors on the same transaction")

	// The node's history window rolls past everything it knows, the cursor included. Every
	// query carrying it is now answered with the not-found error.
	forgottenBeforeSlot := sim.CurrentHeadSlot() + 1
	sim.ForgetSignaturesBeforeSlot(forgottenBeforeSlot)

	finalizedBeforeError, err := run.storage.GetLatestFinalizedBlockNumber()
	require.NoError(t, err)

	time.Sleep(cursorErrorPhase)

	// The tracker has to have recognised the cursor as gone rather than treating it as a hard
	// failure and giving up.
	require.Contains(t, run.logs.String(), "not found, fetching by slot instead",
		"the tracker never recognised its query cursor as one the node no longer knows")

	// And it has to still be working: an error on every query may not wedge the loop.
	finalizedDuringError, err := run.storage.GetLatestFinalizedBlockNumber()
	require.NoError(t, err)
	require.Greater(t, finalizedDuringError, finalizedBeforeError,
		"the polling loop stopped making progress while its cursor was unknown, so it is stuck")

	// The program starts producing again.
	sim.ResumeTransactions()

	require.True(t, run.waitForCondition("the tracker to deliver transactions produced after the cursor was forgotten",
		cursorTimeout, func() bool { return run.deliveredCount() > deliveredBeforeQuiet }),
		"the tracker never delivered anything again after its query cursor was forgotten, "+
			"it delivered %d events and stopped", deliveredBeforeQuiet)

	// Let it settle so more than the first transaction goes through the recovered path.
	time.Sleep(cursorQuietPhase)
	run.stopTracker()

	require.Greater(t, len(run.delivered), deliveredBeforeQuiet,
		"nothing was delivered after the recovery")

	// Recovering may not cost or repeat an event, and the queried transactions must be the
	// ones the chain produced, in order.
	requireDeliveredPrefix(t, run)
	requireKeptUpWithChain(t, run)
	requireTxStateConsistent(t, run)
	requireChainHeadAdvanced(t, run)

	// The cursor it resumes from has to be a transaction the node still knows about, otherwise
	// the next query walks into the same error again.
	latestQueriedAfter, err := run.storage.GetLatestQueriedTransaction()
	require.NoError(t, err)
	require.GreaterOrEqual(t, latestQueriedAfter.Slot, forgottenBeforeSlot,
		"the tracker is still pointing at a transaction outside the node's history window")

	t.Logf("delivered %d events before the program went quiet and %d in total; "+
		"cursor moved from slot %d to %d across the forgotten window at slot %d",
		deliveredBeforeQuiet, len(run.delivered), latestQueriedBefore.Slot, latestQueriedAfter.Slot,
		forgottenBeforeSlot)
}

// TestEventTracker_FiltersRequeriedSameSlotTransactions covers the narrow case the duplicate
// filter exists for. Three transactions share one slot: one is still queued, one is the last
// processed, one was processed before it. When the tracker loses its query cursor it falls back
// to querying by slot and keeps everything from the cursor's slot onwards, which hands back all
// three of them, the two it is already done with included. Only the duplicate filter stands
// between those two and being processed a second time.
func TestEventTracker_FiltersRequeriedSameSlotTransactions(t *testing.T) {
	cfg := trackertest.DefaultConfig()
	// The chain has to stand still. The filter looks the processed signatures up by the slot
	// the tracker's chain head is on, so the head has to stay on the slot the three share.
	cfg.SlotDuration = time.Hour
	// No history either, so the three transactions sharing the slot are the only ones there
	// are and a query answers with exactly them.
	cfg.HistorySlots = 0

	sim := trackertest.NewChainSimulator(t, cfg)

	sharedSlot := sim.ProduceSlotWithTransactions(sameSlotTxCount)

	sameSlotTxs := sim.ProducedTxsFromSlot(sharedSlot)
	require.Len(t, sameSlotTxs, sameSlotTxCount,
		"the three transactions were supposed to land in slot %d together", sharedSlot)

	// Oldest first, so: processed a while ago, processed last, still waiting.
	processedEarlier, lastProcessed, stillQueued := sameSlotTxs[0], sameSlotTxs[1], sameSlotTxs[2]

	run := newSimRun(t, sim, runConfig{
		startFromSlot:  sharedSlot,
		expectFromSlot: sharedSlot,
		// The tracker only reports a filtered duplicate at debug level.
		logLevel: hclog.Debug,
	})

	// A database caught mid-slot: two of the three are done and have their events stored, the
	// third is queued and the cursor sits on it.
	requireEventStored(t, run, sharedSlot, processedEarlier)
	requireEventStored(t, run, sharedSlot, lastProcessed)
	require.NoError(t, run.storage.SetLastProcessedTransaction(txPointOf(lastProcessed)))
	require.NoError(t, run.storage.PushUnprocessedTransactions([]store.TxPoint{txPointOf(stillQueued)}))
	require.NoError(t, run.storage.StoreLatestQueriedTransaction(txPointOf(stillQueued)))

	run.startTracker()

	// The queued one goes through first, which leaves the cursor and the last processed
	// transaction on the same signature, so the tracker goes back to querying.
	run.waitForDelivered(1, cursorTimeout)
	require.True(t, run.waitForCondition("the queued transaction to be finalized", cursorTimeout,
		func() bool {
			point, err := run.storage.GetLastProcessedTransaction()

			return err == nil && point.TxSignature == stillQueued.Signature
		}),
		"the queued transaction was never processed, so the tracker never went back to querying")

	// The node loses that signature while its two slot mates survive, so the next query has
	// no cursor it can use and falls back to querying by slot.
	sim.ForgetSignature(stillQueued.Signature)

	// That fallback hands back the two transactions the tracker is already done with, and the
	// duplicate filter has to recognise both of them.
	require.True(t, run.waitForCondition("the tracker to filter the processed slot mates out",
		cursorTimeout, func() bool {
			return strings.Count(run.logs.String(), "Skipping duplicate transaction signature") >=
				sameSlotTxCount-1
		}),
		"the tracker never filtered the transactions it had already processed after re-querying "+
			"their slot")

	// Let it churn through that answer several times over.
	time.Sleep(cursorSettlePhase)
	run.stopTracker()

	// The re-query really did hand both of them back: a query carrying the lost cursor was
	// refused, and the cursorless one that followed answered with the two slot mates.
	requireSlotMatesWereRequeried(t, run, stillQueued, sameSlotTxCount-1)

	// Only the queued transaction was ever reported, and only once.
	require.Len(t, run.delivered, 1, "the tracker reported transactions it had already processed")
	require.Equal(t, stillQueued.Signature, run.delivered[0].TxSignature)

	// Nothing was queued again, so nothing is waiting to be processed a second time.
	unprocessed, err := run.storage.GetAllUnprocessedTransactions()
	require.NoError(t, err)
	require.Empty(t, unprocessed, "the transactions the tracker had already processed were queued again")

	// And the slot still holds one event per transaction rather than a second copy of any.
	records, err := run.storage.GetEventsBySlot(sharedSlot)
	require.NoError(t, err)
	require.Len(t, records, sameSlotTxCount, "slot %d holds duplicate event records", sharedSlot)

	requireTxStateConsistent(t, run)

	t.Logf("re-queried %d transactions sharing slot %d after losing the cursor; delivered %d event, %d stored",
		sameSlotTxCount-1, sharedSlot, len(run.delivered), len(records))
}

// requireSlotMatesWereRequeried checks the node really did hand the already-processed
// transactions back: a query carrying the lost cursor was refused, and the cursorless query
// that followed answered with the transactions sharing its slot.
func requireSlotMatesWereRequeried(t *testing.T, run *simRun, lostCursor *trackertest.Tx, want int) {
	t.Helper()

	queries := run.sim.SignatureQueries()

	failedAt := -1

	for i, query := range queries {
		if query.FailedNotFound() && query.Until == lostCursor.Signature {
			failedAt = i

			break
		}
	}

	require.NotEqual(t, -1, failedAt,
		"no query carrying the lost cursor %s was refused, so the tracker never had to fall back",
		lostCursor.Signature)
	require.Greater(t, len(queries), failedAt+1,
		"the tracker stopped querying after the refusal instead of falling back")

	fallback := queries[failedAt+1]
	require.True(t, fallback.Before.IsZero() && fallback.Until.IsZero(),
		"the query after the refusal still carried a cursor")
	require.Equal(t, want, fallback.Returned,
		"the fallback query was supposed to hand back the %d transactions sharing the lost cursor's slot",
		want)
}

// requireEventStored writes the event of a transaction the way the tracker does once it has
// processed it, which is what puts its signature in the set the duplicate filter consults.
func requireEventStored(t *testing.T, run *simRun, slot uint64, tx *trackertest.Tx) {
	t.Helper()

	block := run.sim.BlockAt(slot)
	require.NotNil(t, block)

	event := tx.Event
	require.NoError(t, run.storage.StoreEvent(nil, slot, block.Height, tx.Signature,
		run.sim.ProgramID(), trackedEventName, [32]byte{}, &event))
}

// TestEventTracker_PagesBackAfterForgottenQueryCursor is the forgotten-cursor scenario with a
// backlog behind it: by the time the tracker asks, the node has forgotten its cursor and holds
// more signatures than a single query can answer with. So the tracker has to recover twice over,
// first by falling back to querying without a cursor, then by paging back through the rest of
// the backlog — and while paging it must not pass the forgotten cursor to the node again, since
// that is the very signature the node just refused.
func TestEventTracker_PagesBackAfterForgottenQueryCursor(t *testing.T) {
	cfg := trackertest.DefaultConfig()
	// A backlog deeper than one page, plus the transaction whose signature gets forgotten.
	cfg.HistoryTxCount = catchUpBurstTxCount + 1
	cfg.RPCLatency = 0

	sim := trackertest.NewChainSimulator(t, cfg)

	history := sim.ProducedTxsFromSlot(sim.GenesisSlot())
	require.GreaterOrEqual(t, len(history), catchUpBurstTxCount+1)

	// The cursor the tracker is holding, and the backlog that has piled up behind it.
	cursorTx := history[len(history)-catchUpBurstTxCount-1]
	backlog := history[len(history)-catchUpBurstTxCount:]

	require.Len(t, backlog, catchUpBurstTxCount)
	require.Greater(t, len(backlog), signaturesPerPage,
		"the backlog has to outgrow a single query, otherwise there is nothing to page back through")

	run := newSimRun(t, sim, runConfig{
		startFromChainHead: true,
		// The tracker resumes from the database, so what it owes us is the backlog behind
		// the cursor rather than everything from its configured start slot.
		expectFromSlot: backlog[0].Slot,
		rpcLimits:      generousRPCMethodLimits(),
	})

	// A database that got as far as the cursor, and a node that has since forgotten it.
	require.NoError(t, run.storage.SetLastProcessedTransaction(txPointOf(cursorTx)))
	require.NoError(t, run.storage.StoreLatestQueriedTransaction(txPointOf(cursorTx)))
	sim.ForgetSignaturesBeforeSlot(backlog[0].Slot)

	run.startTracker()
	run.waitForDelivered(len(backlog), catchUpBurstTimeout)
	time.Sleep(cursorSettlePhase)
	run.stopTracker()

	requireForgottenCursorPagedBack(t, run, cursorTx)

	// Nothing in the backlog may be missed, repeated or reordered, across the page boundary
	// included: the pages come back newest first and have to end up delivered oldest first.
	requireDeliveredPrefix(t, run)
	require.GreaterOrEqual(t, len(run.delivered), len(backlog),
		"tracker delivered %d of the %d transactions in the backlog", len(run.delivered), len(backlog))
	requireKeptUpWithChain(t, run)
	requireTxStateConsistent(t, run)

	t.Logf("recovered a %d transaction backlog behind a forgotten cursor at slot %d; delivered %d events",
		len(backlog), cursorTx.Slot, len(run.delivered))
}

// requireForgottenCursorPagedBack checks the query sequence the tracker actually issued: the
// query holding the forgotten cursor, the cursorless fallback that came back full, and then the
// paging back through the rest of the backlog with no until cursor at all.
func requireForgottenCursorPagedBack(t *testing.T, run *simRun, cursorTx *trackertest.Tx) {
	t.Helper()

	queries := run.sim.SignatureQueries()

	failedAt := -1

	for i, query := range queries {
		if query.FailedNotFound() && query.Until == cursorTx.Signature {
			failedAt = i

			break
		}
	}

	require.NotEqual(t, -1, failedAt,
		"no query was ever answered with the not-found error for the forgotten cursor %s",
		cursorTx.Signature)
	require.Greater(t, len(queries), failedAt+2,
		"the tracker stopped querying after the not-found error instead of falling back")

	// Straight after the failure it has to ask again without any cursor at all, and that
	// answer comes back full, which is what tells it there is more behind it.
	fallback := queries[failedAt+1]
	require.True(t, fallback.Before.IsZero(), "the fallback query should not carry a before cursor")
	require.True(t, fallback.Until.IsZero(), "the fallback query should not carry an until cursor")
	require.Equal(t, signaturesPerPage, fallback.Returned,
		"the fallback query did not come back full, so the tracker had no reason to page back")

	// Then it pages back from where that answer ended. The until cursor has to stay unset:
	// passing the forgotten signature again would be answered with the same error, which is
	// how this used to stall.
	page := queries[failedAt+2]
	require.Equal(t, fallback.OldestReturned, page.Before,
		"paging did not resume from where the fallback query ended")
	require.True(t, page.Until.IsZero(),
		"paging still carried the forgotten cursor %s as its until, which the node cannot answer",
		cursorTx.Signature)
	require.False(t, page.FailedNotFound(), "the paging query was answered with the not-found error")
	require.NotZero(t, page.Returned, "paging came back empty, so the rest of the backlog was never fetched")

	t.Logf("query sequence: not-found on the forgotten cursor, then %d without cursors, then %d paging back from %s",
		fallback.Returned, page.Returned, page.Before)
}

// TestEventTracker_StoresBlockhashesForSparseSlots covers the other shape a sparse stretch can
// take: the few blocks in it land on the slots the tracker rounds to, one per threshold, rather
// than bunched together off the threshold. Nothing stalls here, because every block in the
// stretch is a slot the tracker was going to ask for anyway. What the test is really after is
// whether all of them make it into the database with their blockhashes, since those are what a
// consumer looks a block up by.
func TestEventTracker_StoresBlockhashesForSparseSlots(t *testing.T) {
	cfg := trackertest.DefaultConfig()
	// The history below is scripted slot by slot; Config.EmptySlotEvery still shapes the
	// stretch after it, where most but not all slots carry a block.
	cfg.HistorySlots = 0

	sim := trackertest.NewChainSimulator(t, cfg)
	genesis := sim.GenesisSlot()

	// A block in every slot up to the sparse stretch.
	sim.ProduceSlotsWithBlocks(sparseRegionStartOffset)

	// Then one block per threshold and nothing in between: +110, +120, and so on to the end of
	// the stretch.
	thresholdSlots := make([]uint64, 0, sparseRegionThresholdBlocks)

	for i := uint64(0); i < sparseRegionThresholdBlocks; i++ {
		sim.ProduceEmptySlots(defaultBlockRoundingThreshold - 1)
		sim.ProduceSlotsWithBlocks(1)

		thresholdSlots = append(thresholdSlots, sim.CurrentHeadSlot())
	}

	// Almost every slot again after the stretch.
	sim.ProduceSlots(sparseRegionTailSlots)

	require.Len(t, thresholdSlots, sparseRegionThresholdBlocks)

	for _, slot := range thresholdSlots {
		require.Zero(t, slot%defaultBlockRoundingThreshold,
			"slot %d is supposed to land on the rounding threshold", slot)
		require.NotNil(t, sim.BlockAt(slot), "the scripted block at slot %d is missing", slot)
	}

	sparseRegionEnd := thresholdSlots[len(thresholdSlots)-1]

	// Start at the first slot of the chain, so the head has to walk the whole stretch.
	run := newSimRun(t, sim, runConfig{
		startFromSlot: genesis,
		rpcLimits:     generousRPCMethodLimits(),
	})

	run.startTracker()

	require.True(t, run.waitForCondition("the tracker to index past the sparse stretch",
		outageTimeout, func() bool {
			blockPoint, err := run.storage.GetLatestBlockPoint()

			return err == nil && blockPoint != nil && blockPoint.BlockSlot > sparseRegionEnd
		}),
		"the tracker never indexed past the sparse stretch ending at slot %d", sparseRegionEnd)

	// This shape needs no escape hatch: each block in the stretch is a slot the tracker rounds
	// to, so the head has something to advance to on every query.
	require.Zero(t, strings.Count(run.logs.String(), "Chain head stuck, force advancing"),
		"the tracker had to force advance across a stretch whose blocks are all on the threshold")

	time.Sleep(outageLivePhase)
	run.stopTracker()

	// Every block in the stretch has to be in the database, reachable both by its slot and by
	// its hash, and carrying the right block number.
	for _, slot := range thresholdSlots {
		block := sim.BlockAt(slot)

		storedHash, err := run.storage.GetBlockhashBySlot(slot)
		require.NoError(t, err, "no block hash stored for slot %d", slot)
		require.Equal(t, block.Hash, storedHash,
			"slot %d resolved to another block's hash, so its own was never stored", slot)

		storedNumber, err := run.storage.GetBlockNumberByBlockhash(block.Hash)
		require.NoError(t, err, "the hash of the block at slot %d is not indexed", slot)
		require.Equal(t, block.Height, storedNumber, "wrong block number stored for slot %d", slot)
	}

	// A slot inside the stretch that never had a block resolves back to the last one that did,
	// which is how a consumer asking about an empty slot is answered.
	emptySlotInStretch := thresholdSlots[0] - 1
	require.Nil(t, sim.BlockAt(emptySlotInStretch))

	storedHash, err := run.storage.GetBlockhashBySlot(emptySlotInStretch)
	require.NoError(t, err)
	require.Equal(t, sim.BlockAt(genesis+sparseRegionStartOffset).Hash, storedHash,
		"an empty slot in the stretch should resolve to the last block before it")

	requireDeliveredPrefix(t, run)
	requireTxStateConsistent(t, run)
	requireChainHeadAdvanced(t, run)

	t.Logf("indexed %d sparse blocks at slots %v; delivered %d of %d events",
		len(thresholdSlots), thresholdSlots, len(run.delivered), len(run.expected))
}

// requireSparseRegionShape checks the scripted history really is the shape the test needs: a
// handful of blocks at the start of the stretch and nothing for the rest of it.
func requireSparseRegionShape(t *testing.T, sim *trackertest.ChainSimulator, from, to uint64) {
	t.Helper()

	blocks := make([]uint64, 0, sparseRegionBlockCount)

	for slot := from + 1; slot <= to; slot++ {
		if sim.BlockAt(slot) != nil {
			blocks = append(blocks, slot)
		}
	}

	require.Len(t, blocks, sparseRegionBlockCount,
		"the sparse stretch is supposed to hold exactly %d blocks, it holds %v",
		sparseRegionBlockCount, blocks)

	// The empty run has to be shorter than the queried window, otherwise this is the live
	// outage scenario instead: a window with nothing in it at all.
	emptyRun := to - blocks[len(blocks)-1]
	require.Less(t, emptyRun, uint64(chainHeadWindowSlots),
		"the empty run of %d slots is longer than the queried window, so no window is ever full",
		emptyRun)

	require.NotNil(t, sim.BlockAt(to+1), "the chain is supposed to be producing again after the stretch")
}

// TestEventTracker_UnsticksChainHeadAfterLiveOutage runs the tracker against a normal chain,
// then has the chain skip a run of slots longer than the window the tracker queries, so a whole
// getBlocks call comes back empty and its chain head has nothing to advance to. The tracker
// has to force its way across the gap and pick up normal operation on the other side, without
// losing any of the transactions that land before or after it.
func TestEventTracker_UnsticksChainHeadAfterLiveOutage(t *testing.T) {
	// Mainnet slot timing and the rate limits a deployment gets by default, so the pace the
	// tracker is answered at is the pace a real node answers at. That matters here: the
	// chain produces slots faster than the tracker queries them, and the recovery must not
	// depend on the tracker being quicker than the chain.
	sim := trackertest.NewChainSimulator(t, trackertest.DefaultConfig())
	run := newSimRun(t, sim, runConfig{
		startFromChainHead: true,
		rpcLimits:          trackertest.ProductionRPCMethodLimits(),
	})

	// Normal operation first, so the chain head is tracking the tip when the outage hits.
	run.startTracker()
	time.Sleep(outageLivePhase)

	blockPointBeforeOutage := requireLatestBlockPoint(t, run)
	require.NotEmpty(t, run.deliveredCount(), "nothing was delivered before the outage")

	// The outage: a run of slots with no blocks at all, longer than the queried window.
	gapStartSlot := sim.CurrentHeadSlot()
	sim.SkipNextSlots(outageGapSlots)

	gapEndSlot := gapStartSlot + outageGapSlots

	// Let the chain come out of the outage and start producing blocks again.
	require.True(t, run.waitForCondition("the chain to produce past the outage", outageTimeout,
		func() bool { return sim.CurrentHeadSlot() > gapEndSlot+outageSettleSlots }),
		"the simulated chain never made it past the outage")

	// The gap really was empty, otherwise the tracker was never stuck to begin with.
	require.Nil(t, sim.BlockAt(gapStartSlot+outageGapSlots/2),
		"the middle of the outage has a block in it, so the window was never empty")

	// Now the tracker has to climb out: its chain head must end up past the gap.
	recovered := run.waitForCondition("the tracker to advance its chain head past the outage",
		outageTimeout, func() bool {
			blockPoint, err := run.storage.GetLatestBlockPoint()

			return err == nil && blockPoint != nil && blockPoint.BlockSlot > gapEndSlot
		})

	require.True(t, recovered,
		"the tracker never advanced its chain head past the outage, it stayed stuck at slot %d",
		blockPointBeforeOutage.BlockSlot)

	// Give it a moment to settle back into normal operation before looking at the result.
	time.Sleep(outageLivePhase)
	run.stopTracker()

	// It has to have gone through the force-advance path, not simply waited the gap out.
	require.Contains(t, run.logs.String(), "Chain head stuck, force advancing",
		"the tracker crossed the outage without ever force advancing its chain head")

	// Back on track: the head is a real block past the gap and keeping up with the tip.
	blockPointAfterOutage := requireLatestBlockPoint(t, run)
	require.Greater(t, blockPointAfterOutage.BlockSlot, gapEndSlot)
	requireChainHeadAdvanced(t, run)

	// The outage cost no transactions: the ones before and after it all arrived, in order
	// and exactly once.
	requireDeliveredPrefix(t, run)
	requireKeptUpWithChain(t, run)
	requireTxStateConsistent(t, run)

	deliveredAfterOutage := 0

	for _, event := range run.delivered {
		if event.SlotNumber > gapEndSlot {
			deliveredAfterOutage++
		}
	}

	require.NotZero(t, deliveredAfterOutage,
		"no transactions from after the outage were delivered, so the tracker never resumed")

	t.Logf("chain head went from slot %d to %d across a %d slot outage; %d of %d events came after it",
		blockPointBeforeOutage.BlockSlot, blockPointAfterOutage.BlockSlot, outageGapSlots,
		deliveredAfterOutage, len(run.delivered))
}

// requireLatestBlockPoint returns the stored chain head, failing if there is none.
func requireLatestBlockPoint(t *testing.T, run *simRun) *store.BlockPoint {
	t.Helper()

	blockPoint, err := run.storage.GetLatestBlockPoint()
	require.NoError(t, err)
	require.NotNil(t, blockPoint, "no chain head was stored")

	return blockPoint
}

func txPointOf(tx *trackertest.Tx) store.TxPoint {
	return store.TxPoint{TxSignature: tx.Signature, Slot: tx.Slot}
}

// txsProducedBetween returns the transactions produced within a time window, which is how the
// restart test identifies what the tracker was not running for.
func txsProducedBetween(txs []*trackertest.Tx, from, to time.Time) []*trackertest.Tx {
	result := make([]*trackertest.Tx, 0, len(txs))

	for _, tx := range txs {
		if !tx.ProducedAt.Before(from) && !tx.ProducedAt.After(to) {
			result = append(result, tx)
		}
	}

	return result
}

// runConfig is the test-side setup for a tracker run against a simulated chain.
type runConfig struct {
	// duration is how long the tracker runs. With waitForEvents set it is the timeout
	// rather than the runtime.
	duration time.Duration

	// waitForEvents, when above zero, stops the tracker as soon as that many events have
	// been delivered instead of always running for the full duration.
	waitForEvents int

	// startFromSlot is passed to the tracker as-is. Zero means unset, which makes the
	// tracker index the program's entire history.
	startFromSlot uint64

	// startFromChainHead replaces startFromSlot with the chain head at startup, i.e. the
	// "only report what happens from now on" configuration.
	startFromChainHead bool

	// expectFromSlot overrides which produced transactions the run expects to be delivered.
	// Use it when the database already carries progress, so the tracker resumes from what it
	// finds there rather than from startFromSlot.
	expectFromSlot uint64

	// rpcLimits overrides the per-method RPC rate limits. Defaults to
	// trackertest.RPCMethodLimits.
	rpcLimits *common.RPCMethodLimitsConfig

	// blockRoundingThreshold is passed to the tracker; defaults to 10.
	blockRoundingThreshold uint64

	// logLevel is the level the tracker logs at, defaulting to Info. Raise it to Debug for
	// runs that assert on something the tracker only reports at that level.
	logLevel hclog.Level
}

// simRun drives a tracker against a simulated chain. The chain, the storage and the
// subscriber outlive the tracker itself, so the tracker can be stopped and started again the
// way a restarted process would be: same database, same event stream, fresh tracker.
type simRun struct {
	t   *testing.T
	sim *trackertest.ChainSimulator
	cfg runConfig

	client     *rpc.Client
	storage    store.StorageHandler
	subscriber *trackertest.CollectingSubscriber
	logs       *trackertest.SyncBuffer

	startFromSlot uint64

	// cancel and stopped belong to the running tracker; both are nil while it is stopped.
	cancel  context.CancelFunc
	stopped chan struct{}

	// Snapshot of the chain and the delivered stream, refreshed on every stop.
	expected  []*trackertest.Tx
	delivered []tracker.EventNotification
	stopTime  time.Time
}

// newSimRun wires up the chain, the storage and the subscriber, and starts the chain
// producing. The tracker is not started yet.
func newSimRun(t *testing.T, sim *trackertest.ChainSimulator, cfg runConfig) *simRun {
	t.Helper()

	run := &simRun{
		t:          t,
		sim:        sim,
		cfg:        cfg,
		client:     sim.StartRPCServer(),
		storage:    trackertest.NewBoltStorage(t),
		subscriber: &trackertest.CollectingSubscriber{},
		logs:       new(trackertest.SyncBuffer),
	}

	sim.Start()

	run.startFromSlot = cfg.startFromSlot
	if cfg.startFromChainHead {
		// Anything the chain produced before this slot is history the tracker must skip.
		run.startFromSlot = sim.CurrentHeadSlot()
	}

	t.Cleanup(func() {
		// A test that fails mid-run would otherwise leave the tracker writing to a
		// storage handler that is about to be closed.
		run.stopTrackerIfRunning()

		if t.Failed() {
			t.Logf("tracker logs:\n%s", run.logs.String())
		}
	})

	return run
}

// startTracker builds a tracker from the run's configuration and starts it against the
// existing storage, which is what a restarted process does: everything it knows about its
// progress has to come back out of the database.
func (r *simRun) startTracker() {
	require.Nil(r.t, r.cancel, "the tracker is already running")

	rpcLimits := r.cfg.rpcLimits
	if rpcLimits == nil {
		rpcLimits = trackertest.RPCMethodLimits()
	}

	blockRoundingThreshold := r.cfg.blockRoundingThreshold
	if blockRoundingThreshold == 0 {
		blockRoundingThreshold = defaultBlockRoundingThreshold
	}

	logLevel := r.cfg.logLevel
	if logLevel == hclog.NoLevel {
		logLevel = hclog.Info
	}

	specs := new(tracker.ProgramEventSpecs)
	_, err := specs.AddEventSpec(skyline_program.HotWalletIncrementEvent{}, trackedEventName)
	require.NoError(r.t, err)

	eventTracker, err := tracker.NewEventTracker(&tracker.EventTrackerConfig{
		Client:                 r.client,
		RPCMethodLimitsConfig:  rpcLimits,
		TrackedPrograms:        map[string]tracker.ProgramEventSpecs{r.sim.ProgramID().String(): *specs},
		Commitment:             "confirmed",
		Logger:                 hclog.New(&hclog.LoggerOptions{Name: "tracker", Level: logLevel, Output: r.logs}),
		RetryTimeout:           200 * time.Millisecond,
		StartFromSlot:          r.startFromSlot,
		BlockRoundingThreshold: blockRoundingThreshold,
		EventSubscriber:        r.subscriber,
	}, r.storage)
	require.NoError(r.t, err)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	r.cancel = cancel
	r.stopped = stopped

	go func() {
		defer close(stopped)

		eventTracker.Start(ctx)
	}()
}

// stopTracker stops the running tracker and refreshes the snapshot the assertions read. The
// chain keeps producing while the tracker is down, so whatever it misses has to be picked up
// by the next startTracker.
func (r *simRun) stopTracker() {
	require.NotNil(r.t, r.cancel, "the tracker is not running")

	r.stopTime = time.Now().UTC()

	r.cancel()

	select {
	case <-r.stopped:
	case <-time.After(30 * time.Second):
		r.t.Fatal("tracker did not stop within 30s of context cancellation")
	}

	r.cancel = nil
	r.stopped = nil

	r.expected = r.expectedNow()
	r.delivered = r.subscriber.Events()

	r.t.Logf("chain produced %d tracked transactions from slot %d, tracker delivered %d events; rpc calls: %v",
		len(r.expected), r.startFromSlot, len(r.delivered), r.sim.RPCCallCounts())
}

func (r *simRun) stopTrackerIfRunning() {
	if r.cancel != nil {
		r.stopTracker()
	}
}

// deliveredCount reports how many events have been delivered so far, including while the
// tracker is running.
func (r *simRun) deliveredCount() int {
	return len(r.subscriber.Events())
}

// expectedNow returns the transactions the tracker is expected to report as of right now,
// rather than as of the last stop.
func (r *simRun) expectedNow() []*trackertest.Tx {
	from := r.startFromSlot
	if r.cfg.expectFromSlot > 0 {
		from = r.cfg.expectFromSlot
	}

	return r.sim.ProducedTxsFromSlot(from)
}

// wait blocks for the configured duration, or until enough events have been delivered when
// the run is waiting on a count.
func (r *simRun) wait() {
	if r.cfg.waitForEvents <= 0 {
		time.Sleep(r.cfg.duration)

		return
	}

	r.waitForDelivered(r.cfg.waitForEvents, r.cfg.duration)
}

// waitForDelivered blocks until at least count events have been delivered, or the timeout
// expires. Timing out is not a failure in itself; the assertions report what is missing.
func (r *simRun) waitForDelivered(count int, timeout time.Duration) {
	r.waitForCondition(fmt.Sprintf("%d events to be delivered", count), timeout, func() bool {
		return r.deliveredCount() >= count
	})
}

// waitForCondition polls until cond holds, and reports whether it did before the timeout.
// Timing out is not a failure in itself; the caller decides what it means.
func (r *simRun) waitForCondition(what string, timeout time.Duration, cond func() bool) bool {
	started := time.Now().UTC()
	deadline := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)

	defer ticker.Stop()

	for {
		if cond() {
			r.t.Logf("waited %s for %s", time.Since(started), what)

			return true
		}

		select {
		case <-deadline:
			r.t.Logf("gave up waiting for %s after %s (%d events delivered)",
				what, timeout, r.deliveredCount())

			return false
		case <-ticker.C:
		}
	}
}

// runTracker starts the chain and the tracker, runs them as configured, then stops the
// tracker and reports what the chain produced against what was delivered.
func runTracker(t *testing.T, sim *trackertest.ChainSimulator, cfg runConfig) *simRun {
	t.Helper()

	run := newSimRun(t, sim, cfg)
	run.startTracker()
	run.wait()
	run.stopTracker()

	return run
}

// generousRPCMethodLimits removes rate limiting as a bottleneck for runs that have to drain
// a large backlog, while leaving the limiter itself in the path.
func generousRPCMethodLimits() *common.RPCMethodLimitsConfig {
	limits := trackertest.RPCMethodLimits()

	const perMethod = 100_000

	limits.GetTransaction = perMethod
	limits.GetBlock = perMethod
	limits.GetBlocks = perMethod
	limits.GetBlockHeight = perMethod
	limits.GetSignaturesForAddress = perMethod

	return limits
}

// requireStartupPagedThroughHistory checks the tracker paged backwards through a history
// deeper than one signature page, walked all of it, and never had to do it again.
func requireStartupPagedThroughHistory(t *testing.T, run *simRun, historyTxCount int) {
	t.Helper()

	queries := run.sim.SignatureQueries()
	require.NotEmpty(t, queries, "the tracker never queried signatures")

	// On a first run there is no processed transaction to stop at, so the initial query and
	// every page after it are sent without an until cursor. That is what separates startup
	// paging from the live queries that follow, which all carry one.
	startupQueries := queries

	for i, query := range queries {
		if !query.Until.IsZero() {
			startupQueries = queries[:i]

			break
		}
	}

	t.Logf("history held %d transactions; startup took %d signature queries out of %d total",
		historyTxCount, len(startupQueries), len(queries))

	require.Greater(t, len(startupQueries), 1,
		"the tracker did not page past the first %d signatures", signaturesPerPage)

	// The first query opens with no cursors at all and must come back full, otherwise the
	// history was not deep enough to require paging in the first place.
	require.True(t, startupQueries[0].Before.IsZero(), "the first query should not have a before cursor")
	require.True(t, startupQueries[0].Until.IsZero(), "the first query should not have an until cursor")
	require.Equal(t, signaturesPerPage, startupQueries[0].Returned,
		"the first query should have filled a whole page")

	// Every following page must resume exactly where the previous one ended.
	fetched := startupQueries[0].Returned

	for i, query := range startupQueries[1:] {
		require.Equal(t, startupQueries[i].OldestReturned, query.Before,
			"page %d did not resume from where page %d ended", i+1, i)

		fetched += query.Returned
	}

	// Paging must have covered the whole history, not just the pages it happened to walk.
	require.GreaterOrEqual(t, fetched, historyTxCount,
		"paging stopped after %d signatures but the history held %d", fetched, historyTxCount)
	require.Less(t, startupQueries[len(startupQueries)-1].Returned, signaturesPerPage,
		"paging stopped on a full page, so it stopped short of the end of the history")

	// After startup the tracker must never page backwards again: the queries that follow
	// only move the until cursor forward. A second catch-up would mean it lost track of
	// what it had already queried.
	for i, query := range queries[len(startupQueries):] {
		require.True(t, query.Before.IsZero(),
			"query %d paged backwards again after startup", i+len(startupQueries))
		require.False(t, query.Until.IsZero(),
			"query %d has no until cursor, so it re-read the history from the start",
			i+len(startupQueries))
	}
}

// requireDeliveredPrefix checks the delivered events are an in-order, duplicate-free prefix
// of what the chain produced, with the payloads decoded correctly.
func requireDeliveredPrefix(t *testing.T, run *simRun) {
	t.Helper()

	require.NotEmpty(t, run.delivered, "tracker delivered no events")
	require.LessOrEqual(t, len(run.delivered), len(run.expected),
		"tracker delivered more events than the chain produced")

	for i, event := range run.delivered {
		want := run.expected[i]

		require.Equal(t, want.Signature, event.TxSignature, "event %d: unexpected transaction signature", i)
		require.Equal(t, want.Slot, event.SlotNumber, "event %d: unexpected slot", i)
		require.Equal(t, trackedEventName, event.EventName, "event %d: unexpected event name", i)
		require.Equal(t, run.sim.ProgramID(), event.Program, "event %d: unexpected program", i)

		payload, ok := event.EventData.(*skyline_program.HotWalletIncrementEvent)
		require.True(t, ok, "event %d: unexpected event data type %T", i, event.EventData)
		require.Equal(t, want.Event, *payload, "event %d: event payload mismatch", i)

		block := run.sim.BlockAt(want.Slot)
		require.NotNil(t, block, "event %d: no simulated block at slot %d", i, want.Slot)
		require.Equal(t, block.Height, event.BlockNumber, "event %d: unexpected block number", i)
	}
}

// requireKeptUpWithChain checks the tracker is not lagging arbitrarily far behind the chain:
// everything produced before the lag allowance window has to be delivered by shutdown. It
// only applies to runs that are expected to be caught up, not to ones draining a backlog.
func requireKeptUpWithChain(t *testing.T, run *simRun) {
	t.Helper()

	deliveredCount := len(run.delivered)
	cutoff := run.stopTime.Add(-deliveryLagAllowance)

	for i, tx := range run.expected {
		if i < deliveredCount {
			continue
		}

		require.False(t, tx.ProducedAt.Before(cutoff),
			"transaction %s (slot %d, produced %s before shutdown) was never delivered; tracker delivered %d of %d",
			tx.Signature, tx.Slot, run.stopTime.Sub(tx.ProducedAt), deliveredCount, len(run.expected))
	}
}

// requireTxStateConsistent checks the persisted transaction and event state matches what was
// delivered, so a restart would resume from the right place.
func requireTxStateConsistent(t *testing.T, run *simRun) {
	t.Helper()

	lastDelivered := run.delivered[len(run.delivered)-1]

	lastProcessed, err := run.storage.GetLastProcessedTransaction()
	require.NoError(t, err)

	// The last delivered event is finalized right after it is dispatched, so at
	// cancellation the queue head is either that transaction or the one before it.
	acceptable := []solana.Signature{lastDelivered.TxSignature}
	if len(run.delivered) > 1 {
		acceptable = append(acceptable, run.delivered[len(run.delivered)-2].TxSignature)
	}

	require.Contains(t, acceptable, lastProcessed.TxSignature,
		"last processed transaction is neither the last nor the second to last delivered event")

	latestQueried, err := run.storage.GetLatestQueriedTransaction()
	require.NoError(t, err)
	require.GreaterOrEqual(t, latestQueried.Slot, lastProcessed.Slot,
		"latest queried transaction is older than the last processed one")

	// Every delivered event must be persisted for its slot.
	for _, event := range run.delivered {
		records, err := run.storage.GetEventsBySlot(event.SlotNumber)
		require.NoError(t, err)

		found := false

		for _, record := range records {
			if record.TxSignature == event.TxSignature.String() && record.EventType == event.EventName {
				found = true

				break
			}
		}

		require.True(t, found, "event of tx %s at slot %d was not persisted",
			event.TxSignature, event.SlotNumber)
	}

	finalizedBlockNumber, err := run.storage.GetLatestFinalizedBlockNumber()
	require.NoError(t, err)
	require.Greater(t, finalizedBlockNumber, uint64(0), "no finalized block number was stored")
}

// requireChainHeadAdvanced checks the stored chain head is a real block that is not trailing
// the simulated head by more than the allowance, which is what a stuck head would look like.
func requireChainHeadAdvanced(t *testing.T, run *simRun) {
	t.Helper()

	latestBlockPoint, err := run.storage.GetLatestBlockPoint()
	require.NoError(t, err)
	require.NotNil(t, latestBlockPoint, "no latest block point was stored")

	headSlot := run.sim.CurrentHeadSlot()

	require.Greater(t, latestBlockPoint.BlockSlot, uint64(0))
	require.LessOrEqual(t, headSlot-latestBlockPoint.BlockSlot, uint64(chainHeadLagAllowanceSlots),
		"stored chain head at slot %d trails the simulated head at slot %d",
		latestBlockPoint.BlockSlot, headSlot)

	block := run.sim.BlockAt(latestBlockPoint.BlockSlot)
	require.NotNil(t, block, "stored chain head points at a slot with no block")
	require.Equal(t, block.Hash, latestBlockPoint.BlockHash)
	require.Equal(t, block.Height, latestBlockPoint.BlockNumber)
}
