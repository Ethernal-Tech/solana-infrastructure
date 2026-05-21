package tracker

import (
	"encoding/binary"
	"fmt"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx"
	"github.com/gagliardetto/solana-go"
)

const (
	ed25519HeaderSize       = 2
	ed25519OffsetsStructLen = 14
	ed25519MsgOffsetField   = 8
	ed25519MsgSizeField     = 10
)

// FindEd25519InstructionData returns the data of the first Ed25519 signature
// verification instruction in the transaction, if present.
func FindEd25519InstructionData(
	instructions []solana.CompiledInstruction,
	accountKeys solana.PublicKeySlice,
) ([]byte, error) {
	for _, ix := range instructions {
		if int(ix.ProgramIDIndex) >= len(accountKeys) {
			continue
		}

		if accountKeys[ix.ProgramIDIndex] == sendtx.Ed25519ProgramID {
			return ix.Data, nil
		}
	}

	return nil, fmt.Errorf("ed25519 instruction not found")
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
func ExtractSolanaPayloadFromEd25519(data []byte) (*sendtx.SolanaPayload, error) {
	if len(data) < ed25519HeaderSize+ed25519OffsetsStructLen {
		return nil, fmt.Errorf("ed25519 data too short: got %d bytes", len(data))
	}

	numSigs := data[0]
	if numSigs == 0 {
		return nil, fmt.Errorf("ed25519 instruction has 0 signatures")
	}

	offsetsStart := ed25519HeaderSize
	msgOffset := binary.LittleEndian.Uint16(data[offsetsStart+ed25519MsgOffsetField:])
	msgSize := binary.LittleEndian.Uint16(data[offsetsStart+ed25519MsgSizeField:])

	msgEnd := int(msgOffset) + int(msgSize)
	if msgEnd > len(data) {
		return nil, fmt.Errorf(
			"ed25519 message range [%d..%d] exceeds data length %d",
			msgOffset, msgEnd, len(data))
	}

	var payload sendtx.SolanaPayload

	if err := payload.Unmarshal(data[msgOffset:msgEnd]); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SolanaPayload from ed25519 message: %w", err)
	}

	return &payload, nil
}
