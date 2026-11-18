package skyline_program

import (
	"testing"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
)

// anchor-go omits the discriminator for instructions without arguments, so
// NewBridgeTransactionInstruction carries a hand edit that writes it. This
// file is not generated: it fails if a regeneration drops that edit again.
func TestBridgeTransactionInstruction_HasDiscriminator(t *testing.T) {
	key := solanago.NewWallet().PublicKey()

	ix, err := NewBridgeTransactionInstruction(key, key, key, key, key, key, key, key, key)
	require.NoError(t, err)

	data, err := ix.Data()
	require.NoError(t, err)
	require.Equal(t, Instruction_BridgeTransaction[:], data)
}
