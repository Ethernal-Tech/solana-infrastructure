package tracker

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// eventAuthoritySeed is the seed of Anchor's event authority PDA, the account a program signs its
// emit_cpi! self-CPI with (anchor-lang event-cpi feature).
var eventAuthoritySeed = []byte("__event_authority")

// eventIxTagLE is Anchor's EVENT_IX_TAG (0x1d9acb512ea545e4) in little-endian. emit_cpi! sends a self-CPI
// whose instruction data is this tag followed by the event bytes (8-byte event discriminator and the
// borsh-encoded event), the same bytes emit! writes to a "Program data:" log line.
var eventIxTagLE = func() []byte {
	tag := make([]byte, 8)
	binary.LittleEndian.PutUint64(tag, 0x1d9acb512ea545e4)

	return tag
}()

// deriveEventAuthority returns the event authority PDA of the given program.
func deriveEventAuthority(programID solana.PublicKey) (solana.PublicKey, error) {
	pda, _, err := solana.FindProgramAddress([][]byte{eventAuthoritySeed}, programID)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("failed to derive event authority PDA for %s: %w", programID, err)
	}

	return pda, nil
}

// transactionAccountKeys returns every account key the transaction's instructions can index: the
// static keys from the message followed by the keys loaded from address lookup tables (writable
// first, then read-only), which is the order inner instruction account indexes refer to.
func transactionAccountKeys(staticKeys solana.PublicKeySlice, loaded rpc.LoadedAddresses) solana.PublicKeySlice {
	keys := make(solana.PublicKeySlice, 0, len(staticKeys)+len(loaded.Writable)+len(loaded.ReadOnly))
	keys = append(keys, staticKeys...)
	keys = append(keys, loaded.Writable...)
	keys = append(keys, loaded.ReadOnly...)

	return keys
}

// collectCPIEvents returns the events recorded by emit_cpi! in a transaction's inner instructions.
//
// emit_cpi! records an event as a self-CPI: the program invokes itself with the event authority PDA as
// a signer and the instruction data EVENT_IX_TAG || event bytes. The program's event handler rejects
// that call unless the event authority signed and is the program's PDA, and only the program itself can
// sign for its PDA. A self-CPI that failed would have failed the whole transaction, and failed
// transactions are skipped before this is reached. So in a successful transaction every inner
// instruction of this shape was emitted by that program.
//
// An inner instruction is accepted when it invokes a tracked program, its data starts with the event
// tag and its first account is that program's event authority PDA. eventAuthorityOf returns the event
// authority of a tracked program, and false for any other program. Each returned event records the
// index of the top-level instruction it was emitted under.
//
// Inner instructions are part of the transaction metadata and are not subject to the log size limit,
// so unlike "Program data:" log lines they cannot be truncated away.
func collectCPIEvents(
	innerInstructions []rpc.InnerInstruction,
	keys solana.PublicKeySlice,
	eventAuthorityOf func(solana.PublicKey) (solana.PublicKey, bool),
) []emittedEvent {
	var events []emittedEvent

	for _, group := range innerInstructions {
		for _, ix := range group.Instructions {
			if int(ix.ProgramIDIndex) >= len(keys) {
				continue
			}

			programID := keys[ix.ProgramIDIndex]

			eventAuthority, tracked := eventAuthorityOf(programID)
			if !tracked {
				continue
			}

			data := []byte(ix.Data)
			if len(data) <= len(eventIxTagLE) || !bytes.Equal(data[:len(eventIxTagLE)], eventIxTagLE) {
				continue
			}

			if len(ix.Accounts) == 0 || int(ix.Accounts[0]) >= len(keys) ||
				!keys[ix.Accounts[0]].Equals(eventAuthority) {
				continue
			}

			events = append(events, emittedEvent{
				programID:     programID,
				data:          data[len(eventIxTagLE):],
				topLevelIndex: int(group.Index),
			})
		}
	}

	return events
}

// findBatchPayloadAtInstruction returns the validator-signed SolanaPayload of the batch executed under
// the top-level instruction at topLevelIndex, mirroring the program's verify_ed25519_batch: it takes
// the ed25519 instruction directly before that instruction, or if there is none, the one directly
// after it. verify_ed25519_batch works on top-level instruction indexes (the instructions sysvar),
// so this also holds when bridge_transaction was reached through a CPI. The payload's batch id must
// equal the event's.
func findBatchPayloadAtInstruction(
	instructions []solana.CompiledInstruction,
	keys solana.PublicKeySlice,
	topLevelIndex int,
	eventBatchID uint64,
) (*sendtx.SolanaPayload, error) {
	if topLevelIndex < 0 || topLevelIndex >= len(instructions) {
		return nil, fmt.Errorf("event instruction index %d out of range (%d instructions)",
			topLevelIndex, len(instructions))
	}

	for _, adjacent := range []int{topLevelIndex - 1, topLevelIndex + 1} {
		if !instructionProgramEquals(instructions, keys, adjacent, sendtx.Ed25519ProgramID) {
			continue
		}

		// A transaction holds far fewer than 65536 instructions, so the index fits in a u16.
		payload, err := ExtractSolanaPayloadFromEd25519(
			instructions[adjacent].Data, uint16(adjacent)) //nolint:gosec // see above
		if err != nil {
			return nil, fmt.Errorf("failed to extract batch payload from ed25519 instruction %d: %w", adjacent, err)
		}

		if payload.BatchID != eventBatchID {
			return nil, fmt.Errorf("ed25519 instruction %d carries batch id %d, event reports %d",
				adjacent, payload.BatchID, eventBatchID)
		}

		return payload, nil
	}

	return nil, fmt.Errorf("no ed25519 instruction adjacent to instruction %d", topLevelIndex)
}
