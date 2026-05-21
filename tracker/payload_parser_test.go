package tracker

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
)

func buildTestEd25519InstructionData(t *testing.T, payloadBytes []byte, numSigs int) []byte {
	t.Helper()

	const (
		pubKeyLength = 32
		sigLength    = 64
	)

	data := make([]byte, 0,
		ed25519HeaderSize+numSigs*ed25519OffsetsStructLen+numSigs*(pubKeyLength+sigLength)+len(payloadBytes))
	data = append(data, byte(numSigs), 0)

	baseOffset := ed25519HeaderSize + numSigs*ed25519OffsetsStructLen
	sharedMsgOffset := baseOffset + numSigs*(pubKeyLength+sigLength)

	for i := 0; i < numSigs; i++ {
		payloadOffset := baseOffset + i*(pubKeyLength+sigLength)
		signatureOffset := payloadOffset + pubKeyLength

		offsets := make([]byte, ed25519OffsetsStructLen)
		binary.LittleEndian.PutUint16(offsets[0:], uint16(signatureOffset))
		binary.LittleEndian.PutUint16(offsets[2:], 0xFFFF)
		binary.LittleEndian.PutUint16(offsets[4:], uint16(payloadOffset))
		binary.LittleEndian.PutUint16(offsets[6:], 0xFFFF)
		binary.LittleEndian.PutUint16(offsets[8:], uint16(sharedMsgOffset))
		binary.LittleEndian.PutUint16(offsets[10:], uint16(len(payloadBytes)))
		binary.LittleEndian.PutUint16(offsets[12:], 0xFFFF)
		data = append(data, offsets...)
	}

	for i := 0; i < numSigs; i++ {
		data = append(data, make([]byte, pubKeyLength)...)
		data = append(data, make([]byte, sigLength)...)
	}

	data = append(data, payloadBytes...)

	return data
}

func TestExtractSolanaPayloadFromEd25519(t *testing.T) {
	t.Parallel()

	receiverWallet, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	blockHashWallet, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	original := sendtx.SolanaPayload{
		Blockhash: [32]byte(blockHashWallet.PublicKey()),
		Receivers: []sendtx.PayloadReceiver{
			{
				Address: [32]byte(receiverWallet.PublicKey()),
				TokenAmount: wallet.TokenAmount{
					TokenID: 25,
					Amount:  1_000_000_000,
				},
			},
		},
		FeeAmount: 1_000_000_000,
		BatchID:   1,
	}

	payloadBytes, err := original.Marshal()
	require.NoError(t, err)

	ed25519Data := buildTestEd25519InstructionData(t, payloadBytes, 1)

	got, err := ExtractSolanaPayloadFromEd25519(ed25519Data)
	require.NoError(t, err)
	require.Equal(t, original, *got)
}

func TestExtractSolanaPayloadFromEd25519_ProductionPayload(t *testing.T) {
	t.Parallel()

	raw, err := hex.DecodeString(
		"bc69d80eedd69641ee4109ea79868425a3b4946afa2e5683ee946b262b659729" +
			"0168c61393628c69f2dd67c2a6754c1b3a706deae20f659f55432ef9cbc24d" +
			"0198190000ca9a3b0000000000ca9a3b000000000100000000000000",
	)
	require.NoError(t, err)

	ed25519Data := buildTestEd25519InstructionData(t, raw, 2)

	got, err := ExtractSolanaPayloadFromEd25519(ed25519Data)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.BatchID)
	require.Equal(t, uint64(1_000_000_000), got.FeeAmount)
	require.Len(t, got.Receivers, 1)
	require.Equal(t, uint16(25), got.Receivers[0].TokenAmount.TokenID)
}

func TestFindEd25519InstructionData(t *testing.T) {
	t.Parallel()

	otherProgram := solana.NewWallet().PublicKey()
	ed25519Data := []byte{1, 0, 1, 2, 3}

	instructions := []solana.CompiledInstruction{
		{ProgramIDIndex: 0, Data: []byte{9, 9, 9}},
		{ProgramIDIndex: 1, Data: ed25519Data},
	}
	accountKeys := solana.PublicKeySlice{otherProgram, sendtx.Ed25519ProgramID}

	got, err := FindEd25519InstructionData(instructions, accountKeys)
	require.NoError(t, err)
	require.Equal(t, ed25519Data, got)
}

func TestFindEd25519InstructionData_NotFound(t *testing.T) {
	t.Parallel()

	instructions := []solana.CompiledInstruction{
		{ProgramIDIndex: 0, Data: []byte{1, 2, 3}},
	}
	accountKeys := solana.PublicKeySlice{solana.NewWallet().PublicKey()}

	_, err := FindEd25519InstructionData(instructions, accountKeys)
	require.Error(t, err)
}

func TestExtractSolanaPayloadFromEd25519_TooShort(t *testing.T) {
	t.Parallel()

	_, err := ExtractSolanaPayloadFromEd25519([]byte{1})
	require.Error(t, err)
}
