package sendtx

import (
	"encoding/hex"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
)

func TestSolanaPayload_MarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()

	receiverWallet, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	blockHashWallet, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	original := SolanaPayload{
		Blockhash: [32]byte(blockHashWallet.PublicKey()),
		Receivers: []PayloadReceiver{
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

	raw, err := original.Marshal()
	require.NoError(t, err)

	var decoded SolanaPayload

	require.NoError(t, decoded.Unmarshal(raw))
	require.Equal(t, original, decoded)
}

func TestSolanaPayload_UnmarshalProductionPayload(t *testing.T) {
	t.Parallel()

	raw, err := hex.DecodeString(
		"bc69d80eedd69641ee4109ea79868425a3b4946afa2e5683ee946b262b659729" +
			"0168c61393628c69f2dd67c2a6754c1b3a706deae20f659f55432ef9cbc24d" +
			"0198190000ca9a3b0000000000ca9a3b000000000100000000000000",
	)
	require.NoError(t, err)

	var payload SolanaPayload

	require.NoError(t, payload.Unmarshal(raw))
	require.Equal(t, uint64(1), payload.BatchID)
	require.Equal(t, uint64(1_000_000_000), payload.FeeAmount)
	require.Len(t, payload.Receivers, 1)
	require.Equal(t, uint16(25), payload.Receivers[0].TokenAmount.TokenID)
	require.Equal(t, uint64(1_000_000_000), payload.Receivers[0].TokenAmount.Amount)
	require.NotEqual(t, [32]byte{}, payload.Blockhash)
}
