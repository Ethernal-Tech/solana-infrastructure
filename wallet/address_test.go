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

func TestValidatePublicKey(t *testing.T) {
	// Setup valid on-curve key
	validOnCurve := solana.MustPublicKeyFromBase58("vines1vzrYbzLMRdu58ou5XTby4qAqVRLmqo36NKPTg")

	// Setup PDA (off-curve key)
	seed := []byte("seed")
	programID := solana.SystemProgramID
	pdaAddress, _, err := solana.FindProgramAddress([][]byte{seed}, programID)
	require.NoError(t, err)

	t.Run("valid on-curve key with off-curve rejected", func(t *testing.T) {
		err := ValidatePublicKey(validOnCurve, false)
		require.NoError(t, err)
	})

	t.Run("valid on-curve key with off-curve accepted", func(t *testing.T) {
		err := ValidatePublicKey(validOnCurve, true)
		require.NoError(t, err)
	})

	t.Run("PDA with off-curve rejected", func(t *testing.T) {
		err := ValidatePublicKey(pdaAddress, false)
		require.Error(t, err, "off-curve")
	})

	t.Run("PDA with off-curve accepted", func(t *testing.T) {
		err := ValidatePublicKey(pdaAddress, true)
		require.NoError(t, err)
	})

	t.Run("zero key", func(t *testing.T) {
		err := ValidatePublicKey(solana.PublicKey{}, false)
		require.Error(t, err, "cannot be zero")
	})
}

func TestPublicKeyFromAddress(t *testing.T) {
	t.Run("valid base58 address", func(t *testing.T) {
		address := "vines1vzrYbzLMRdu58ou5XTby4qAqVRLmqo36NKPTg"

		result, err := PublicKeyFromAddress(address)

		require.NoError(t, err)
		require.Equal(t, address, result.String())
		require.False(t, result.IsZero())
	})

	t.Run("valid system program address", func(t *testing.T) {
		address := "11111111111111111111111111111111"

		result, err := PublicKeyFromAddress(address)

		require.NoError(t, err)
		require.Equal(t, address, result.String())
		require.True(t, result.IsZero())
	})

	t.Run("invalid base58 character", func(t *testing.T) {
		address := "vines1vzrYbzLMRdu58ou5XTby4qAqVRLmqo36NKPTl!!!"

		result, err := PublicKeyFromAddress(address)

		require.Error(t, err)
		require.Equal(t, solana.PublicKey{}, result)
		require.Contains(t, err.Error(), "invalid address")
	})

	t.Run("empty string", func(t *testing.T) {
		address := ""

		result, err := PublicKeyFromAddress(address)

		require.Error(t, err)
		require.Equal(t, solana.PublicKey{}, result)
		require.Contains(t, err.Error(), "invalid address")
	})

	t.Run("too short - valid base58 but wrong length", func(t *testing.T) {
		address := "1" // Decodes to 1 byte

		result, err := PublicKeyFromAddress(address)

		require.Error(t, err)
		require.Equal(t, solana.PublicKey{}, result)
		require.Contains(t, err.Error(), "invalid address")
	})

	t.Run("too long - valid base58 but wrong length", func(t *testing.T) {
		address := "11111111111111111111111111111111111111111111"

		result, err := PublicKeyFromAddress(address)

		require.Error(t, err)
		require.Equal(t, solana.PublicKey{}, result)
		require.Contains(t, err.Error(), "invalid address")
	})

	t.Run("invalid base58 - contains invalid characters (I, O, l)", func(t *testing.T) {
		address := "vines1vzrYbzLMRdu58ou5XTby4qAqVRLmqo36NKPTl" // 'l' is invalid in base58

		result, err := PublicKeyFromAddress(address)

		require.Error(t, err)
		require.Equal(t, solana.PublicKey{}, result)
		require.Contains(t, err.Error(), "invalid address")
	})

	t.Run("contains leading spaces", func(t *testing.T) {
		address := " vines1vzrYbzLMRdu58ou5XTby4qAqVRLmqo36NKPTg"

		result, err := PublicKeyFromAddress(address)

		require.Error(t, err)
		require.Equal(t, solana.PublicKey{}, result)
		require.Contains(t, err.Error(), "invalid address")
	})

	t.Run("contains trailing spaces", func(t *testing.T) {
		address := "vines1vzrYbzLMRdu58ou5XTby4qAqVRLmqo36NKPTg "

		result, err := PublicKeyFromAddress(address)

		require.Error(t, err)
		require.Equal(t, solana.PublicKey{}, result)
		require.Contains(t, err.Error(), "invalid address")
	})
}
