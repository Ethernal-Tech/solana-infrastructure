package tracker

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx"
	"github.com/gagliardetto/solana-go"
)

// Ed25519 verify instruction layout. Each offsets entry is 7 little-endian u16 fields.
const (
	ed25519HeaderSize       = 2
	ed25519OffsetsStructLen = 14

	ed25519SigOffsetField    = 0
	ed25519SigIxField        = 2
	ed25519PubKeyOffsetField = 4
	ed25519PubKeyIxField     = 6
	ed25519MsgOffsetField    = 8
	ed25519MsgSizeField      = 10
	ed25519MsgIxField        = 12

	ed25519SigLength    = 64
	ed25519PubKeyLength = 32

	// ed25519CurrentInstruction in an instruction-index field means "this instruction".
	ed25519CurrentInstruction = 0xFFFF
)

// instructionProgramEquals reports whether the instruction at idx is invoked by programID.
func instructionProgramEquals(
	instructions []solana.CompiledInstruction, accountKeys solana.PublicKeySlice,
	idx int, programID solana.PublicKey,
) bool {
	if idx < 0 || idx >= len(instructions) {
		return false
	}

	ix := instructions[idx]
	if int(ix.ProgramIDIndex) >= len(accountKeys) {
		return false
	}

	return accountKeys[ix.ProgramIDIndex].Equals(programID)
}

// ExtractSolanaPayloadFromEd25519 decodes the validator-signed SolanaPayload
// embedded as the shared message in a batched Ed25519 verify instruction.
//
// Layout (see sendtx.TxSender.buildBatchedEd25519Instruction):
//
//	[num_sigs:u8][pad:u8]
//	[offsets:14 bytes] * num_sigs
//	[pubkey:32][sig:64] * num_sigs
//	[message: borsh-encoded SolanaPayload]
//
// It applies the same rules as the program's parse_ed25519_batch, so it only ever decodes a payload the
// program would accept: every signature, public key and message offset must point into this instruction
// (0xFFFF, or ixIndex, its own index in the transaction), every offset range must lie within the data,
// and every signature must sign the same message. Offsets that point into another instruction are
// rejected rather than followed.
func ExtractSolanaPayloadFromEd25519(data []byte, ixIndex uint16) (*sendtx.SolanaPayload, error) {
	if len(data) < ed25519HeaderSize {
		return nil, fmt.Errorf("ed25519 data too short: got %d bytes", len(data))
	}

	numSigs := int(data[0])
	if numSigs == 0 {
		return nil, fmt.Errorf("ed25519 instruction has 0 signatures")
	}

	isOwnInstruction := func(idx uint16) bool {
		return idx == ed25519CurrentInstruction || idx == ixIndex
	}

	var sharedMessage []byte

	for i := 0; i < numSigs; i++ {
		entry := ed25519HeaderSize + i*ed25519OffsetsStructLen
		if entry+ed25519OffsetsStructLen > len(data) {
			return nil, fmt.Errorf("ed25519 offsets entry %d exceeds data length %d", i, len(data))
		}

		field := func(offset int) uint16 {
			return binary.LittleEndian.Uint16(data[entry+offset:])
		}

		sigOffset := int(field(ed25519SigOffsetField))
		pubKeyOffset := int(field(ed25519PubKeyOffsetField))
		msgOffset := int(field(ed25519MsgOffsetField))
		msgSize := int(field(ed25519MsgSizeField))

		if !isOwnInstruction(field(ed25519SigIxField)) ||
			!isOwnInstruction(field(ed25519PubKeyIxField)) ||
			!isOwnInstruction(field(ed25519MsgIxField)) {
			return nil, fmt.Errorf(
				"ed25519 offsets entry %d references another instruction (sig %d, pubkey %d, message %d)",
				i, field(ed25519SigIxField), field(ed25519PubKeyIxField), field(ed25519MsgIxField))
		}

		if sigOffset+ed25519SigLength > len(data) ||
			pubKeyOffset+ed25519PubKeyLength > len(data) ||
			msgOffset+msgSize > len(data) {
			return nil, fmt.Errorf("ed25519 offsets entry %d points outside data length %d", i, len(data))
		}

		message := data[msgOffset : msgOffset+msgSize]

		if sharedMessage == nil {
			sharedMessage = message
		} else if !bytes.Equal(sharedMessage, message) {
			return nil, fmt.Errorf("ed25519 signature %d signs a different message than signature 0", i)
		}
	}

	var payload sendtx.SolanaPayload

	if err := payload.Unmarshal(sharedMessage); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SolanaPayload from ed25519 message: %w", err)
	}

	return &payload, nil
}
