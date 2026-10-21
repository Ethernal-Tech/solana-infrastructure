package tracker

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx"
	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

// cpiEventData is the instruction data emit_cpi! sends: EVENT_IX_TAG followed by the event bytes. The
// tag is spelled out here rather than taken from eventIxTagLE, so a wrong tag in the tracker fails the
// tests instead of cancelling out.
func cpiEventData(event []byte) solana.Base58 {
	tag := []byte{0xe4, 0x45, 0xa5, 0x2e, 0x51, 0xcb, 0x9a, 0x1d} // 0x1d9acb512ea545e4, little-endian

	return append(tag, event...)
}

func TestDeriveEventAuthority(t *testing.T) {
	t.Parallel()

	programID := solana.NewWallet().PublicKey()

	want, _, err := solana.FindProgramAddress([][]byte{[]byte("__event_authority")}, programID)
	require.NoError(t, err)

	got, err := deriveEventAuthority(programID)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestTransactionAccountKeys(t *testing.T) {
	t.Parallel()

	static := solana.PublicKeySlice{solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey()}
	writable := solana.PublicKeySlice{solana.NewWallet().PublicKey()}
	readOnly := solana.PublicKeySlice{solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey()}

	keys := transactionAccountKeys(static, rpc.LoadedAddresses{Writable: writable, ReadOnly: readOnly})

	require.Equal(t, solana.PublicKeySlice{static[0], static[1], writable[0], readOnly[0], readOnly[1]}, keys)
}

// TestCollectCPIEvents checks that only self-CPIs emitted by a tracked program through emit_cpi! are
// read as events.
func TestCollectCPIEvents(t *testing.T) {
	t.Parallel()

	tracked := solana.NewWallet().PublicKey()
	attacker := solana.NewWallet().PublicKey()
	payer := solana.NewWallet().PublicKey()

	trackedAuthority, err := deriveEventAuthority(tracked)
	require.NoError(t, err)

	attackerAuthority, err := deriveEventAuthority(attacker)
	require.NoError(t, err)

	const (
		payerIdx = iota
		trackedIdx
		trackedAuthorityIdx
		attackerIdx
		attackerAuthorityIdx
	)

	keys := solana.PublicKeySlice{payer, tracked, trackedAuthority, attacker, attackerAuthority}

	eventAuthorityOf := func(programID solana.PublicKey) (solana.PublicKey, bool) {
		if programID.Equals(tracked) {
			return trackedAuthority, true
		}

		return solana.PublicKey{}, false
	}

	genuine := []byte("genuine-event-bytes")

	selfCPI := func(programIdx, authorityIdx uint16, event []byte) rpc.CompiledInstruction {
		return rpc.CompiledInstruction{
			ProgramIDIndex: programIdx,
			Accounts:       []uint16{authorityIdx},
			Data:           cpiEventData(event),
		}
	}

	t.Run("accepts an event the tracked program emitted with emit_cpi!", func(t *testing.T) {
		t.Parallel()

		inner := []rpc.InnerInstruction{
			{Index: 3, Instructions: []rpc.CompiledInstruction{selfCPI(trackedIdx, trackedAuthorityIdx, genuine)}},
		}

		events := collectCPIEvents(inner, keys, eventAuthorityOf)

		require.Len(t, events, 1)
		require.True(t, events[0].programID.Equals(tracked))
		require.Equal(t, genuine, events[0].data, "the event tag must be stripped")
		require.Equal(t, 3, events[0].topLevelIndex)
	})

	t.Run("keeps events in order and records each top-level instruction index", func(t *testing.T) {
		t.Parallel()

		first, second, third := []byte("first"), []byte("second"), []byte("third")

		inner := []rpc.InnerInstruction{
			{Index: 0, Instructions: []rpc.CompiledInstruction{
				// unrelated inner instruction before the event
				{ProgramIDIndex: attackerIdx, Data: solana.Base58("noise")},
				selfCPI(trackedIdx, trackedAuthorityIdx, first),
			}},
			{Index: 2, Instructions: []rpc.CompiledInstruction{
				selfCPI(trackedIdx, trackedAuthorityIdx, second),
				selfCPI(trackedIdx, trackedAuthorityIdx, third),
			}},
		}

		events := collectCPIEvents(inner, keys, eventAuthorityOf)

		require.Len(t, events, 3)
		require.Equal(t, first, events[0].data)
		require.Equal(t, 0, events[0].topLevelIndex)
		require.Equal(t, second, events[1].data)
		require.Equal(t, 2, events[1].topLevelIndex)
		require.Equal(t, third, events[2].data)
		require.Equal(t, 2, events[2].topLevelIndex)
	})

	t.Run("resolves program and authority loaded from an address lookup table", func(t *testing.T) {
		t.Parallel()

		static := solana.PublicKeySlice{payer}
		allKeys := transactionAccountKeys(static, rpc.LoadedAddresses{
			Writable: solana.PublicKeySlice{},
			ReadOnly: solana.PublicKeySlice{tracked, trackedAuthority},
		})

		inner := []rpc.InnerInstruction{
			{Index: 0, Instructions: []rpc.CompiledInstruction{selfCPI(1, 2, genuine)}},
		}

		events := collectCPIEvents(inner, allKeys, eventAuthorityOf)

		require.Len(t, events, 1)
		require.Equal(t, genuine, events[0].data)
	})

	rejected := []struct {
		name string
		ix   rpc.CompiledInstruction
	}{
		{
			name: "a program that is not tracked, even with its own valid event authority",
			ix:   selfCPI(attackerIdx, attackerAuthorityIdx, genuine),
		},
		{
			name: "the tracked program invoked with another account in place of its event authority",
			ix:   selfCPI(trackedIdx, attackerAuthorityIdx, genuine),
		},
		{
			name: "the tracked program invoked with no accounts",
			ix:   rpc.CompiledInstruction{ProgramIDIndex: trackedIdx, Data: cpiEventData(genuine)},
		},
		{
			name: "the tracked program invoked without the event tag",
			ix: rpc.CompiledInstruction{
				ProgramIDIndex: trackedIdx,
				Accounts:       []uint16{trackedAuthorityIdx},
				Data:           solana.Base58(append([]byte{1, 2, 3, 4, 5, 6, 7, 8}, genuine...)),
			},
		},
		{
			name: "the event tag with no event after it",
			ix: rpc.CompiledInstruction{
				ProgramIDIndex: trackedIdx,
				Accounts:       []uint16{trackedAuthorityIdx},
				Data:           cpiEventData(nil),
			},
		},
		{
			name: "a program index out of range",
			ix:   selfCPI(uint16(len(keys)), trackedAuthorityIdx, genuine),
		},
		{
			name: "an authority index out of range",
			ix:   selfCPI(trackedIdx, uint16(len(keys)), genuine),
		},
	}

	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()

			inner := []rpc.InnerInstruction{{Index: 0, Instructions: []rpc.CompiledInstruction{tc.ix}}}

			require.Empty(t, collectCPIEvents(inner, keys, eventAuthorityOf))
		})
	}
}

// TestCollectTransactionEvents checks that events are read only from emit_cpi! self-CPIs, and never from
// "Program data:" log lines, which any program in the transaction can write.
func TestCollectTransactionEvents(t *testing.T) {
	t.Parallel()

	tracked := solana.NewWallet().PublicKey()

	trackedAuthority, err := deriveEventAuthority(tracked)
	require.NoError(t, err)

	tracker := &EventTracker{
		trackedPrograms:  map[solana.PublicKey]ProgramEventSpecs{tracked: nil},
		eventAuthorities: map[solana.PublicKey]solana.PublicKey{tracked: trackedAuthority},
		logger:           hclog.NewNullLogger(),
	}

	keys := solana.PublicKeySlice{solana.NewWallet().PublicKey(), tracked, trackedAuthority}

	cpiEvent := []byte("cpi-event")
	logEvent := []byte("log-event")

	// A "Program data:" line framed as emitted by the tracked program itself.
	logs := []string{
		fmt.Sprintf("Program %s invoke [1]", tracked),
		"Program data: " + base64.StdEncoding.EncodeToString(logEvent),
		fmt.Sprintf("Program %s success", tracked),
	}

	selfCPI := []rpc.InnerInstruction{{Index: 0, Instructions: []rpc.CompiledInstruction{{
		ProgramIDIndex: 1,
		Accounts:       []uint16{2},
		Data:           cpiEventData(cpiEvent),
	}}}}

	t.Run("reads the emit_cpi! event and ignores log events", func(t *testing.T) {
		t.Parallel()

		events := tracker.collectTransactionEvents(
			&rpc.TransactionMeta{LogMessages: logs, InnerInstructions: selfCPI}, keys)

		require.Len(t, events, 1)
		require.Equal(t, cpiEvent, events[0].data)
		require.Equal(t, 0, events[0].topLevelIndex)
	})

	t.Run("ignores log events when there is no emit_cpi! event", func(t *testing.T) {
		t.Parallel()

		events := tracker.collectTransactionEvents(
			&rpc.TransactionMeta{LogMessages: logs, InnerInstructions: []rpc.InnerInstruction{}}, keys)

		require.Empty(t, events)
	})

	t.Run("reads the emit_cpi! event when the logs are empty", func(t *testing.T) {
		t.Parallel()

		events := tracker.collectTransactionEvents(&rpc.TransactionMeta{InnerInstructions: selfCPI}, keys)

		require.Len(t, events, 1)
		require.Equal(t, cpiEvent, events[0].data)
	})
}

// TestFindBatchPayloadAtInstruction checks that the batch payload of an emit_cpi! TransactionExecutedEvent
// comes from the ed25519 instruction the program verified for the instruction the event was emitted under.
func TestFindBatchPayloadAtInstruction(t *testing.T) {
	t.Parallel()

	skyline := solana.NewWallet().PublicKey()
	caller := solana.NewWallet().PublicKey()
	keys := solana.PublicKeySlice{skyline, sendtx.Ed25519ProgramID, caller}

	const (
		skylineIdx = 0
		ed25519Idx = 1
		callerIdx  = 2
	)

	payloadFor := func(batchID uint64) sendtx.SolanaPayload {
		return sendtx.SolanaPayload{
			Blockhash: [32]byte(solana.NewWallet().PublicKey()),
			Receivers: []sendtx.PayloadReceiver{{
				Address:     [32]byte(solana.NewWallet().PublicKey()),
				TokenAmount: wallet.TokenAmount{TokenID: 1, Amount: 1_000_000},
			}},
			FeeAmount: 1_000,
			BatchID:   batchID,
		}
	}

	ed25519Ix := func(p sendtx.SolanaPayload) solana.CompiledInstruction {
		raw, err := p.Marshal()
		require.NoError(t, err)

		return solana.CompiledInstruction{ProgramIDIndex: ed25519Idx, Data: buildTestEd25519InstructionData(t, raw, 1)}
	}

	bridgeTxIx := solana.CompiledInstruction{
		ProgramIDIndex: skylineIdx,
		Data:           append(append([]byte{}, skyline_program.Instruction_BridgeTransaction[:]...), 0xAA),
	}

	t.Run("uses the ed25519 instruction before the event's instruction", func(t *testing.T) {
		t.Parallel()

		genuine := payloadFor(7)

		instructions := []solana.CompiledInstruction{
			ed25519Ix(payloadFor(99)), // index 0: forged, not adjacent to the event's instruction
			ed25519Ix(genuine),        // index 1: verified by the program for instruction 2
			bridgeTxIx,                // index 2: the event's instruction
		}

		got, err := findBatchPayloadAtInstruction(instructions, keys, 2, genuine.BatchID)
		require.NoError(t, err)
		require.Equal(t, genuine, *got)
	})

	t.Run("uses the ed25519 instruction after when there is none before", func(t *testing.T) {
		t.Parallel()

		genuine := payloadFor(7)

		instructions := []solana.CompiledInstruction{bridgeTxIx, ed25519Ix(genuine)}

		got, err := findBatchPayloadAtInstruction(instructions, keys, 0, genuine.BatchID)
		require.NoError(t, err)
		require.Equal(t, genuine, *got)
	})

	t.Run("works when bridge_transaction was reached through a CPI", func(t *testing.T) {
		t.Parallel()

		// The top-level instruction is another program that called bridge_transaction. The program
		// verifies the ed25519 instruction adjacent to that top-level instruction.
		genuine := payloadFor(7)

		instructions := []solana.CompiledInstruction{
			ed25519Ix(genuine),
			{ProgramIDIndex: callerIdx, Data: []byte{1, 2, 3}},
		}

		got, err := findBatchPayloadAtInstruction(instructions, keys, 1, genuine.BatchID)
		require.NoError(t, err)
		require.Equal(t, genuine, *got)
	})

	t.Run("rejects a batch id mismatch instead of trying the other side", func(t *testing.T) {
		t.Parallel()

		// The program takes the ed25519 instruction before when there is one, so one after with the
		// matching batch id must not be used.
		instructions := []solana.CompiledInstruction{
			ed25519Ix(payloadFor(8)),
			bridgeTxIx,
			ed25519Ix(payloadFor(7)),
		}

		_, err := findBatchPayloadAtInstruction(instructions, keys, 1, 7)
		require.Error(t, err)
	})

	t.Run("rejects when no ed25519 instruction is adjacent", func(t *testing.T) {
		t.Parallel()

		instructions := []solana.CompiledInstruction{
			ed25519Ix(payloadFor(7)),
			{ProgramIDIndex: callerIdx, Data: []byte{1}},
			bridgeTxIx,
		}

		_, err := findBatchPayloadAtInstruction(instructions, keys, 2, 7)
		require.Error(t, err)
	})

	t.Run("rejects an instruction index out of range", func(t *testing.T) {
		t.Parallel()

		instructions := []solana.CompiledInstruction{bridgeTxIx}

		_, err := findBatchPayloadAtInstruction(instructions, keys, 5, 7)
		require.Error(t, err)

		_, err = findBatchPayloadAtInstruction(instructions, keys, -1, 7)
		require.Error(t, err)
	})
}
