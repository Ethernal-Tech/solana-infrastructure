// Package trackertest provides the test harness for the event tracker: a simulated Solana
// chain served over a real JSON-RPC endpoint, plus the test doubles the tracker needs to
// run against it. It emulates the slice of validator behaviour the tracker depends on:
// slots advance in real time, only some of them carry a block, block heights increment per
// block (not per slot), and tracked-program transactions land in a subset of those blocks
// carrying an Anchor event in their log messages.
//
// The chain state machine lives here; the JSON-RPC surface that exposes it is in
// rpc_server.go and the test doubles are in helpers.go.
package trackertest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	binary "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/require"
)

const (
	// defaultSlotDuration mirrors mainnet's ~400ms slot time.
	defaultSlotDuration = 400 * time.Millisecond

	// defaultHistorySlots is how much chain is generated synchronously before the tracker
	// starts, so the first getSignaturesForAddress query has to skip pre-StartFromSlot
	// history the same way it does against a live chain.
	defaultHistorySlots = 200

	// genesisSlot is the first slot of the simulated chain, chosen high enough to look
	// like a real chain rather than a fresh validator.
	genesisSlot = 340_000_000

	// genesisBlockHeight is the block height of genesisSlot. Real chains skip slots, so
	// height trails the slot number.
	genesisBlockHeight = 320_000_000

	// eventName is the tracked event the simulated program emits.
	eventName = "HotWalletIncrementEvent"
)

// Config parameterises the shape of the simulated chain.
type Config struct {
	// SlotDuration is how long one slot takes.
	SlotDuration time.Duration

	// EmptySlotEvery makes every Nth slot carry no block, emulating a skipped leader.
	EmptySlotEvery uint64

	// TxEvery lands one tracked-program transaction every Nth slot-with-block.
	TxEvery uint64

	// RPCLatency is the artificial delay added to every RPC response.
	RPCLatency time.Duration

	// HistorySlots is how many slots of history are generated before the chain starts
	// producing in real time.
	HistorySlots uint64

	// HistoryTxCount, when set, extends the generated history until it holds at least
	// this many tracked-program transactions. Use it to build a history deeper than the
	// 1000-signature getSignaturesForAddress page, which is what forces the tracker to
	// paginate on startup.
	HistoryTxCount int
}

// DefaultConfig returns a chain that behaves like mainnet: 400ms slots, roughly one in
// seven slots skipped, and a tracked-program transaction in every third block.
func DefaultConfig() Config {
	return Config{
		SlotDuration:   defaultSlotDuration,
		EmptySlotEvery: 7,
		TxEvery:        3,
		RPCLatency:     2 * time.Millisecond,
		HistorySlots:   defaultHistorySlots,
	}
}

// Block is a block produced for a slot. Slots without a Block were skipped.
type Block struct {
	// Slot is the slot the block was produced for.
	Slot uint64

	// Height is the block height, which counts blocks rather than slots.
	Height uint64

	// Hash is the block hash.
	Hash solana.Hash

	parentSlot uint64
	parentHash solana.Hash
	signatures []solana.Signature
	blockTime  time.Time
}

// Tx is a tracked-program transaction, carrying both the serialized transaction the RPC
// hands out and the event the tracker is expected to decode out of its logs.
type Tx struct {
	// Signature is the signature of the signed transaction.
	Signature solana.Signature

	// Slot is the slot the transaction landed in.
	Slot uint64

	// Event is the event the transaction emits, i.e. what the tracker must report.
	Event skyline_program.HotWalletIncrementEvent

	// ProducedAt is when the simulator produced the transaction, used to reason about
	// how far the tracker lags behind the chain.
	ProducedAt time.Time

	raw  []byte
	logs []string
}

// ChainSimulator is a chain that keeps producing slots, blocks and tracked-program
// transactions in real time. It is safe for concurrent use.
type ChainSimulator struct {
	t         *testing.T
	cfg       Config
	programID solana.PublicKey
	payer     *solana.Wallet
	mint      solana.PublicKey

	mu              sync.RWMutex
	headSlot        uint64
	headBlockHeight uint64
	blocks          map[uint64]*Block
	slotsWithBlocks []uint64 // ascending
	txsBySignature  map[solana.Signature]*Tx
	programTxs      []*Tx // production order, oldest first
	blocksProduced  uint64
	rng             *rand.Rand

	pendingSkips        uint64
	txsPaused           bool
	forgottenBeforeSlot uint64
	forgottenSignatures map[solana.Signature]struct{}

	callCounts       map[string]int
	signatureQueries []SignatureQuery

	stop chan struct{}
	done chan struct{}
}

// NewChainSimulator returns a simulator whose chain already has a past, so a tracker
// starting against it has history to skip. Nothing is produced until Start is called.
func NewChainSimulator(t *testing.T, cfg Config) *ChainSimulator {
	t.Helper()

	require.False(t, cfg.TxEvery == 0 && cfg.HistoryTxCount > 0,
		"Config.HistoryTxCount needs Config.TxEvery set, otherwise no transactions are ever produced")

	s := &ChainSimulator{
		t:               t,
		cfg:             cfg,
		programID:       skyline_program.ProgramID,
		payer:           solana.NewWallet(),
		mint:            solana.NewWallet().PublicKey(),
		headSlot:        genesisSlot,
		headBlockHeight: genesisBlockHeight,
		blocks:          make(map[uint64]*Block),
		txsBySignature:  make(map[solana.Signature]*Tx),
		rng:             rand.New(rand.NewSource(1)), //nolint:gosec // deterministic test data
		callCounts:      make(map[string]int),
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}

	for slots := uint64(0); slots < cfg.HistorySlots || s.ProducedTxCount() < cfg.HistoryTxCount; slots++ {
		s.advance()
	}

	return s
}

// Start begins producing slots in real time, until the test finishes.
func (s *ChainSimulator) Start() {
	go func() {
		defer close(s.done)

		ticker := time.NewTicker(s.cfg.SlotDuration)
		defer ticker.Stop()

		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.advance()
			}
		}
	}()

	s.t.Cleanup(func() {
		close(s.stop)
		<-s.done
	})
}

// blockPolicy decides whether the slot being produced carries a block.
type blockPolicy int

const (
	// policyConfigured leaves it to the configuration and any pending skips.
	policyConfigured blockPolicy = iota

	// policyBlock always produces a block, policyEmpty never does.
	policyBlock
	policyEmpty
)

// ProduceSlotsWithBlocks advances the chain by n slots, every one of them carrying a block.
// Together with ProduceEmptySlots it scripts a chain of a specific shape, slot by slot, which
// is how a test reproduces a stretch of chain that once broke something. Both produce their
// slots immediately rather than in real time, so call them before Start.
func (s *ChainSimulator) ProduceSlotsWithBlocks(n uint64) {
	for i := uint64(0); i < n; i++ {
		s.advanceWithPolicy(policyBlock)
	}
}

// ProduceEmptySlots advances the chain by n slots with no block in any of them.
func (s *ChainSimulator) ProduceEmptySlots(n uint64) {
	for i := uint64(0); i < n; i++ {
		s.advanceWithPolicy(policyEmpty)
	}
}

// ProduceSlots advances the chain by n slots following the configuration, so most of them
// carry a block and Config.EmptySlotEvery decides which do not. It is the scripted equivalent
// of letting the chain run normally for n slots.
func (s *ChainSimulator) ProduceSlots(n uint64) {
	for i := uint64(0); i < n; i++ {
		s.advance()
	}
}

// ProduceSlotWithTransactions advances the chain by one slot whose block carries n
// tracked-program transactions, and returns the slot they landed in. Slots normally hold at
// most one, so this is how a test puts several transactions in the same slot.
func (s *ChainSimulator) ProduceSlotWithTransactions(n int) uint64 {
	s.advanceSlot(policyBlock, n)

	return s.CurrentHeadSlot()
}

// txCountFromConfig lets the configuration decide how many transactions a block carries.
const txCountFromConfig = -1

// advance produces the next slot, which may or may not carry a block.
func (s *ChainSimulator) advance() {
	s.advanceWithPolicy(policyConfigured)
}

func (s *ChainSimulator) advanceWithPolicy(policy blockPolicy) {
	s.advanceSlot(policy, txCountFromConfig)
}

func (s *ChainSimulator) advanceSlot(policy blockPolicy, txCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.headSlot++

	switch policy {
	case policyEmpty:
		return
	case policyBlock:
		// The caller wants a block here whatever the configuration says.
	case policyConfigured:
		// An outage requested through SkipNextSlots: the slots pass with nothing in them.
		if s.pendingSkips > 0 {
			s.pendingSkips--

			return
		}

		// Skipped leader: the slot exists but no block was produced for it.
		if s.cfg.EmptySlotEvery > 0 && s.headSlot%s.cfg.EmptySlotEvery == 0 {
			return
		}
	}

	s.headBlockHeight++
	s.blocksProduced++

	block := &Block{
		Slot:       s.headSlot,
		Height:     s.headBlockHeight,
		Hash:       blockhashForSlot(s.headSlot),
		blockTime:  time.Now().UTC(),
		parentSlot: s.headSlot - 1,
		parentHash: blockhashForSlot(s.headSlot - 1),
	}

	if len(s.slotsWithBlocks) > 0 {
		parent := s.slotsWithBlocks[len(s.slotsWithBlocks)-1]
		block.parentSlot = parent
		block.parentHash = blockhashForSlot(parent)
	}

	txsInBlock := txCount
	if txCount == txCountFromConfig {
		txsInBlock = 0

		if s.cfg.TxEvery > 0 && !s.txsPaused && s.blocksProduced%s.cfg.TxEvery == 0 {
			txsInBlock = 1
		}
	}

	for i := 0; i < txsInBlock; i++ {
		tx := s.buildProgramTx(block)
		block.signatures = append(block.signatures, tx.Signature)
		s.txsBySignature[tx.Signature] = tx
		s.programTxs = append(s.programTxs, tx)
	}

	s.blocks[block.Slot] = block
	s.slotsWithBlocks = append(s.slotsWithBlocks, block.Slot)
}

// buildProgramTx assembles a real, signed transaction that invokes the tracked program and
// emits a HotWalletIncrementEvent through a "Program data:" log line, exactly as an Anchor
// program does via emit!.
func (s *ChainSimulator) buildProgramTx(block *Block) *Tx {
	t := s.t
	t.Helper()

	event := skyline_program.HotWalletIncrementEvent{
		Sender: s.payer.PublicKey(),
		Mint:   s.mint,
		Amount: s.rng.Uint64()%1_000_000 + 1,
	}

	// Instruction data of the call itself; opaque to the tracker, which reads events
	// from the logs, not from the instruction payload.
	ixData := make([]byte, 8, 16)
	copy(ixData, []byte{0x7a, 0x11, 0x03, 0x9c, 0x22, 0x5f, 0x1b, 0x0d})
	ixData = appendUint64LE(ixData, event.Amount)

	instructions := []solana.Instruction{
		// Realistic noise: an instruction of a program the tracker does not track.
		solana.NewInstruction(
			solana.MustPublicKeyFromBase58("ComputeBudget111111111111111111111111111111"),
			solana.AccountMetaSlice{},
			[]byte{0x02, 0x40, 0x0d, 0x03, 0x00},
		),
		solana.NewInstruction(
			s.programID,
			solana.AccountMetaSlice{
				{PublicKey: s.payer.PublicKey(), IsSigner: true, IsWritable: true},
				{PublicKey: s.mint, IsSigner: false, IsWritable: false},
			},
			ixData,
		),
	}

	tx, err := solana.NewTransaction(instructions, block.Hash, solana.TransactionPayer(s.payer.PublicKey()))
	require.NoError(t, err)

	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(s.payer.PublicKey()) {
			return &s.payer.PrivateKey
		}

		return nil
	})
	require.NoError(t, err)

	raw, err := tx.MarshalBinary()
	require.NoError(t, err)

	require.NotEmpty(t, tx.Signatures)

	return &Tx{
		Signature:  tx.Signatures[0],
		Slot:       block.Slot,
		Event:      event,
		ProducedAt: time.Now().UTC(),
		raw:        raw,
		logs: []string{
			fmt.Sprintf("Program %s invoke [1]", s.programID),
			"Program log: Instruction: DepositToHotWallet",
			"Program data: " + base64.StdEncoding.EncodeToString(encodeEventLogPayload(t, eventName, event)),
			fmt.Sprintf("Program %s consumed 31337 of 200000 compute units", s.programID),
			fmt.Sprintf("Program %s success", s.programID),
		},
	}
}

// encodeEventLogPayload builds the bytes an Anchor emit! puts into a "Program data:" log:
// the 8-byte event discriminator followed by the borsh-serialized event. The discriminator
// is derived here independently of the tracker's own derivation, so a change to either side
// shows up as a decoding failure instead of cancelling out.
func encodeEventLogPayload(t *testing.T, name string, event any) []byte {
	t.Helper()

	discriminant := sha256.Sum256([]byte("event:" + name))

	buf := new(bytes.Buffer)
	encoder := binary.NewBorshEncoder(buf)

	require.NoError(t, encoder.WriteBytes(discriminant[:8], false))
	require.NoError(t, encoder.Encode(event))

	return buf.Bytes()
}

func appendUint64LE(dst []byte, v uint64) []byte {
	for i := 0; i < 8; i++ {
		dst = append(dst, byte(v>>(8*i)))
	}

	return dst
}

func blockhashForSlot(slot uint64) solana.Hash {
	var hash solana.Hash

	// Deterministic, slot-derived, and distinct per slot.
	for i := 0; i < len(hash); i++ {
		hash[i] = byte(slot >> (8 * (i % 8)))
	}

	hash[8] = byte(slot % 251)

	return hash
}

// ProgramID returns the address of the program whose events the simulated chain emits.
func (s *ChainSimulator) ProgramID() solana.PublicKey {
	return s.programID
}

// PauseTransactions stops the program from producing transactions. Slots and blocks keep
// coming, they just have nothing tracked in them, which is what a quiet program looks like and
// what leaves a tracker's query cursor sitting on the same signature for a long time.
func (s *ChainSimulator) PauseTransactions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.txsPaused = true
}

// ResumeTransactions lets the program produce transactions again.
func (s *ChainSimulator) ResumeTransactions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.txsPaused = false
}

// ForgetSignaturesBeforeSlot moves the node's signature history window forward, so it no longer
// answers for transactions older than the given slot. A node only keeps recent history, so a
// cursor that falls out of that window stops being a signature it knows: getSignaturesForAddress
// then answers a request carrying it with the not-found error rather than with a list, which is
// what the tracker has to recover from.
//
// Only the signature listing honours the window. Transactions already fetched stay fetchable,
// since a tracker never asks for one it has processed again.
func (s *ChainSimulator) ForgetSignaturesBeforeSlot(slot uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.forgottenBeforeSlot = slot
}

// ForgetSignature makes the node stop answering for one specific signature while leaving the
// rest of its slot alone, the way a dropped or rolled back transaction disappears from a slot
// its neighbours survived. A query carrying it as a cursor is answered with the not-found error.
func (s *ChainSimulator) ForgetSignature(signature solana.Signature) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.forgottenSignatures == nil {
		s.forgottenSignatures = make(map[solana.Signature]struct{})
	}

	s.forgottenSignatures[signature] = struct{}{}
}

// visibleProgramTxs returns the transactions the node still answers for, in production order.
// The caller must hold at least a read lock.
func (s *ChainSimulator) visibleProgramTxs() []*Tx {
	if s.forgottenBeforeSlot == 0 && len(s.forgottenSignatures) == 0 {
		return s.programTxs
	}

	visible := make([]*Tx, 0, len(s.programTxs))

	for _, tx := range s.programTxs {
		if tx.Slot < s.forgottenBeforeSlot {
			continue
		}

		if _, forgotten := s.forgottenSignatures[tx.Signature]; forgotten {
			continue
		}

		visible = append(visible, tx)
	}

	return visible
}

// SkipNextSlots makes the chain produce the next n slots with no block in any of them, the
// way an outage leaves a run of empty slots behind. A run longer than the window the tracker
// asks getBlocks for means a whole query comes back empty, so its chain head has nothing to
// advance to. Production resumes as configured once the run is over.
func (s *ChainSimulator) SkipNextSlots(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pendingSkips += n
}

// GenesisSlot returns the first slot of the simulated chain, i.e. the oldest slot any
// transaction can have. Starting a tracker here indexes the whole history without making
// its chain head refresher crawl up from slot zero.
func (s *ChainSimulator) GenesisSlot() uint64 {
	return genesisSlot
}

// CurrentHeadSlot returns the slot the chain has advanced to.
func (s *ChainSimulator) CurrentHeadSlot() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.headSlot
}

// ProducedTxsFromSlot returns, in production order, the tracked-program transactions that
// landed at or after the given slot, i.e. exactly the set a tracker configured with
// StartFromSlot == fromSlot is expected to report.
func (s *ChainSimulator) ProducedTxsFromSlot(fromSlot uint64) []*Tx {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Tx, 0, len(s.programTxs))

	for _, tx := range s.programTxs {
		if tx.Slot >= fromSlot {
			result = append(result, tx)
		}
	}

	return result
}

// ProducedTxCount returns how many tracked-program transactions the chain has produced.
func (s *ChainSimulator) ProducedTxCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.programTxs)
}

// BlockAt returns the block produced for the given slot, or nil if the slot was skipped.
func (s *ChainSimulator) BlockAt(slot uint64) *Block {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.blocks[slot]
}

// RPCCallCounts returns how many times each RPC method has been called, which is useful
// both as a diagnostic and to assert a test actually exercised the path it claims to.
func (s *ChainSimulator) RPCCallCounts() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	counts := make(map[string]int, len(s.callCounts))
	for method, count := range s.callCounts {
		counts[method] = count
	}

	return counts
}

// SignatureQuery records one getSignaturesForAddress call, so a test can assert on the
// cursors the tracker paged through rather than only on the outcome.
type SignatureQuery struct {
	// Before and Until are the cursors the tracker asked with; zero means unset.
	Before solana.Signature
	Until  solana.Signature

	// Limit is the page size the request asked for.
	Limit int

	// Returned is how many signatures the query answered with. A full page means the
	// tracker has to keep paging to reach the end of the history.
	Returned int

	// OldestReturned is the last signature of the answer, i.e. the cursor a caller
	// paging further back is expected to pass as Before next.
	OldestReturned solana.Signature

	// ErrorCode is the JSON-RPC error the query was answered with, or zero if it was
	// answered with a list. A failed query is still recorded, since which cursor the
	// caller was holding when it failed is the interesting part.
	ErrorCode int
}

// FailedNotFound reports whether the query was answered with the error a node returns for a
// signature it does not know, which is what a caller holding a forgotten cursor gets.
func (q SignatureQuery) FailedNotFound() bool {
	return q.ErrorCode == rpcErrCodeTxNotFound
}

// SignatureQueries returns every getSignaturesForAddress call so far, in call order.
func (s *ChainSimulator) SignatureQueries() []SignatureQuery {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return append([]SignatureQuery(nil), s.signatureQueries...)
}

func (s *ChainSimulator) recordSignatureQuery(query SignatureQuery) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.signatureQueries = append(s.signatureQueries, query)
}

// --- chain queries backing the RPC surface ---

func (s *ChainSimulator) getBlocks(startSlot uint64, endSlot *uint64) []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	end := s.headSlot
	if endSlot != nil && *endSlot < end {
		end = *endSlot
	}

	result := make([]uint64, 0, 16)

	for _, slot := range s.slotsWithBlocks {
		if slot < startSlot {
			continue
		}

		if slot > end {
			break
		}

		result = append(result, slot)
	}

	return result
}

func (s *ChainSimulator) getBlockHeight() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.headBlockHeight
}

func (s *ChainSimulator) getTransaction(signature solana.Signature) *Tx {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.txsBySignature[signature]
}

// getSignaturesForAddress mirrors the validator's semantics: newest first, `before` and
// `until` are exclusive cursors, and a cursor the node does not know, either because it never
// existed or because it has fallen out of the history window, is an error rather than an empty
// result.
func (s *ChainSimulator) getSignaturesForAddress(
	address solana.PublicKey, before, until solana.Signature, limit int,
) ([]*rpc.TransactionSignature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !address.Equals(s.programID) {
		return nil, nil
	}

	visible := s.visibleProgramTxs()
	end := len(visible)

	if !before.IsZero() {
		index, found := indexOfSignature(visible, before)
		if !found {
			return nil, signatureNotFoundError(before)
		}

		end = index
	}

	start := 0

	if !until.IsZero() {
		index, found := indexOfSignature(visible, until)
		if !found {
			return nil, signatureNotFoundError(until)
		}

		start = index + 1
	}

	result := make([]*rpc.TransactionSignature, 0, max(end-start, 0))

	for i := end - 1; i >= start && len(result) < limit; i-- {
		tx := visible[i]
		blockTime := solana.UnixTimeSeconds(tx.ProducedAt.Unix())

		result = append(result, &rpc.TransactionSignature{
			Signature:          tx.Signature,
			Slot:               tx.Slot,
			BlockTime:          &blockTime,
			ConfirmationStatus: rpc.ConfirmationStatusFinalized,
		})
	}

	return result, nil
}

// indexOfSignature returns the position of a transaction in production order.
func indexOfSignature(txs []*Tx, signature solana.Signature) (int, bool) {
	for i, tx := range txs {
		if tx.Signature == signature {
			return i, true
		}
	}

	return 0, false
}
