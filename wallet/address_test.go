package wallet

import (
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/test-go/testify/require"
)

func TestValidateAddress(t *testing.T) {
	validKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	validAddress := validKey.PublicKey().String()

	pdaAddress, _, err := solana.FindProgramAddress([][]byte{[]byte("test")}, validKey.PublicKey())
	require.NoError(t, err)

	pdaAddressStr := pdaAddress.String()

	t.Run("empty address", func(t *testing.T) {
		err := ValidateAddress("", false)
		require.Error(t, err, "zero length")
	})

	t.Run("invalid base58", func(t *testing.T) {
		err := ValidateAddress("invalid_base58_address", false)
		require.Error(t, err, "invalid base58")
	})

	t.Run("wrong length", func(t *testing.T) {
		err := ValidateAddress("1111111111111111111111111111111", false)
		require.Error(t, err, "invalid length")
	})

	t.Run("valid on-curve address", func(t *testing.T) {
		err := ValidateAddress(validAddress, false)
		require.NoError(t, err)
	})

	t.Run("valid on-curve address with off-curve accepted", func(t *testing.T) {
		err := ValidateAddress(validAddress, true)
		require.NoError(t, err)
	})

	t.Run("valid PDA with off-curve accepted", func(t *testing.T) {
		err := ValidateAddress(pdaAddressStr, true)
		require.NoError(t, err)
	})

	t.Run("valid PDA with off-curve rejected", func(t *testing.T) {
		err := ValidateAddress(pdaAddressStr, false)
		require.Error(t, err, "public key is off-curve")
	})
}
