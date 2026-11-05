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

	got, err := ExtractSolanaPayloadFromEd25519(ed25519Data, 0)
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

	got, err := ExtractSolanaPayloadFromEd25519(ed25519Data, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.BatchID)
	require.Equal(t, uint64(1_000_000_000), got.FeeAmount)
	require.Len(t, got.Receivers, 1)
	require.Equal(t, uint16(25), got.Receivers[0].TokenAmount.TokenID)
}

func TestExtractSolanaPayloadFromEd25519_TooShort(t *testing.T) {
	t.Parallel()

	_, err := ExtractSolanaPayloadFromEd25519([]byte{1}, 0)
	require.Error(t, err)
}

// TestExtractSolanaPayloadFromEd25519_MatchesProgramRules checks that the decoder only accepts what the
// program's parse_ed25519_batch accepts, so it can never decode a payload the program did not verify.
func TestExtractSolanaPayloadFromEd25519_MatchesProgramRules(t *testing.T) {
	t.Parallel()

	payload := sendtx.SolanaPayload{
		Blockhash: [32]byte(solana.NewWallet().PublicKey()),
		Receivers: []sendtx.PayloadReceiver{{
			Address:     [32]byte(solana.NewWallet().PublicKey()),
			TokenAmount: wallet.TokenAmount{TokenID: 1, Amount: 1_000_000},
		}},
		FeeAmount: 1_000,
		BatchID:   7,
	}

	raw, err := payload.Marshal()
	require.NoError(t, err)

	const (
		numSigs = 3
		ixIndex = 4 // position of the ed25519 instruction in the transaction
	)

	// valid returns fresh, valid instruction data with numSigs signatures over the payload.
	valid := func() []byte {
		return buildTestEd25519InstructionData(t, raw, numSigs)
	}

	// setField overwrites one u16 field of offsets entry sig.
	setField := func(data []byte, sig, field int, value uint16) {
		binary.LittleEndian.PutUint16(data[ed25519HeaderSize+sig*ed25519OffsetsStructLen+field:], value)
	}

	t.Run("accepts the current-instruction marker", func(t *testing.T) {
		t.Parallel()

		got, err := ExtractSolanaPayloadFromEd25519(valid(), ixIndex)
		require.NoError(t, err)
		require.Equal(t, payload, *got)
	})

	t.Run("accepts an explicit reference to its own index", func(t *testing.T) {
		t.Parallel()

		data := valid()
		for sig := 0; sig < numSigs; sig++ {
			setField(data, sig, ed25519SigIxField, ixIndex)
			setField(data, sig, ed25519PubKeyIxField, ixIndex)
			setField(data, sig, ed25519MsgIxField, ixIndex)
		}

		got, err := ExtractSolanaPayloadFromEd25519(data, ixIndex)
		require.NoError(t, err)
		require.Equal(t, payload, *got)
	})

	rejected := []struct {
		name    string
		corrupt func(data []byte) []byte
	}{
		{
			name: "a message stored in another instruction",
			corrupt: func(data []byte) []byte {
				setField(data, 0, ed25519MsgIxField, ixIndex-1)

				return data
			},
		},
		{
			name: "a signature stored in another instruction",
			corrupt: func(data []byte) []byte {
				setField(data, 1, ed25519SigIxField, ixIndex+1)

				return data
			},
		},
		{
			name: "a public key stored in another instruction",
			corrupt: func(data []byte) []byte {
				setField(data, 2, ed25519PubKeyIxField, 0)

				return data
			},
		},
		{
			name: "a later signature over a different message",
			corrupt: func(data []byte) []byte {
				// Point the last signature at one byte less of the same message.
				msgSize := binary.LittleEndian.Uint16(
					data[ed25519HeaderSize+(numSigs-1)*ed25519OffsetsStructLen+ed25519MsgSizeField:])
				setField(data, numSigs-1, ed25519MsgSizeField, msgSize-1)

				return data
			},
		},
		{
			name: "a signature offset beyond the data",
			corrupt: func(data []byte) []byte {
				setField(data, 1, ed25519SigOffsetField, uint16(len(data)-ed25519SigLength+1))

				return data
			},
		},
		{
			name: "a public key offset beyond the data",
			corrupt: func(data []byte) []byte {
				setField(data, 0, ed25519PubKeyOffsetField, uint16(len(data)))

				return data
			},
		},
		{
			name: "an offsets table longer than the data",
			corrupt: func(data []byte) []byte {
				data[0] = 200

				return data
			},
		},
		{
			name: "zero signatures",
			corrupt: func(data []byte) []byte {
				data[0] = 0

				return data
			},
		},
	}

	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ExtractSolanaPayloadFromEd25519(tc.corrupt(valid()), ixIndex)
			require.Error(t, err)
		})
	}
}
