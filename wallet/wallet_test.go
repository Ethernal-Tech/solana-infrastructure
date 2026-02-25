package wallet

import (
	"crypto/ed25519"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/test-go/testify/assert"
	"github.com/test-go/testify/require"
)

func TestNewWallet(t *testing.T) {
	t.Run("create a new random wallet", func(t *testing.T) {
		wallet, err := NewWallet()
		require.NoError(t, err)
		require.NotNil(t, wallet)
		require.NotEmpty(t, wallet.PublicKey)
		require.NotEmpty(t, wallet.PrivateKey)

		require.True(t, wallet.PrivateKey.IsValid())
		require.False(t, wallet.PublicKey.IsZero())

		require.Equal(t, wallet.PrivateKey.PublicKey(), wallet.PublicKey)
		require.NoError(t, wallet.PrivateKey.Validate())
	})
}

func TestNewWalletFromPrivateKey(t *testing.T) {
	privateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	privateKeyStr := privateKey.String()

	t.Run("create a wallet from a valid private key", func(t *testing.T) {
		wallet, err := NewWalletFromPrivateKey(privateKeyStr)
		require.NoError(t, err)
		require.NotNil(t, wallet)
		require.Equal(t, privateKey.PublicKey(), wallet.PublicKey)
		require.Equal(t, privateKey, wallet.PrivateKey)
	})

	t.Run("empty private key", func(t *testing.T) {
		_, err := NewWalletFromPrivateKey("")
		require.Error(t, err, "failed to create wallet from private key")
	})

	t.Run("invalid private key", func(t *testing.T) {
		_, err := NewWalletFromPrivateKey("invalidPrivateKey")
		require.Error(t, err, "failed to create wallet from private key")
	})

	t.Run("wrong length private key", func(t *testing.T) {
		_, err := NewWalletFromPrivateKey("11111111111111111111111111111111")
		require.Error(t, err, "failed to create wallet from private key")
	})
}

func TestWalletSign(t *testing.T) {
	wallet, err := NewWallet()
	require.NoError(t, err)

	t.Run("sign empty payload", func(t *testing.T) {
		payload := []byte{}

		signature, err := wallet.Sign(payload)
		require.NoError(t, err)
		require.NotNil(t, signature)
		require.Len(t, signature, ed25519.SignatureSize)

		require.True(t, wallet.PublicKey.Verify(payload, *signature))
	})

	t.Run("sign small payload", func(t *testing.T) {
		payload := []byte("small payload")

		signature, err := wallet.Sign(payload)
		require.NoError(t, err)
		require.NotNil(t, signature)
		require.Len(t, signature, ed25519.SignatureSize)

		require.True(t, wallet.PublicKey.Verify(payload, *signature))
	})

	t.Run("sign large payload", func(t *testing.T) {
		payload := make([]byte, 1024)

		signature, err := wallet.Sign(payload)
		require.NoError(t, err)
		require.NotNil(t, signature)
		require.Len(t, signature, ed25519.SignatureSize)

		require.True(t, wallet.PublicKey.Verify(payload, *signature))
	})
}

func TestWalletVerify(t *testing.T) {
	wallet, err := NewWallet()
	require.NoError(t, err)

	validPayload := []byte("test message")

	validSignature, err := wallet.Sign(validPayload)
	require.NoError(t, err)
	require.NotNil(t, validSignature)

	t.Run("verify valid signature", func(t *testing.T) {
		err := wallet.VerifySignature(validPayload, *validSignature)
		require.NoError(t, err)
	})

	t.Run("wrong payload", func(t *testing.T) {
		err := wallet.VerifySignature([]byte("wrong message"), *validSignature)
		require.Error(t, err, ErrInvalidSignature)
	})

	t.Run("wrong signature", func(t *testing.T) {
		err = wallet.VerifySignature(validPayload, solana.Signature{})
		require.Error(t, err, ErrInvalidSignature)
	})

	t.Run("signature from different wallet", func(t *testing.T) {
		otherWallet, err := NewWallet()
		require.NoError(t, err)

		sig, err := otherWallet.Sign(validPayload)
		require.NoError(t, err)
		require.NotNil(t, sig)

		err = wallet.VerifySignature(validPayload, *sig)
		require.Error(t, err, ErrInvalidSignature)
	})
}

func TestWalletGetKeys(t *testing.T) {
	wallet, err := NewWallet()
	require.NoError(t, err)

	privateKey, publicKey := wallet.GetKeys()

	assert.Equal(t, wallet.PrivateKey, privateKey)
	assert.Equal(t, wallet.PublicKey, publicKey)
	assert.True(t, privateKey.IsValid())
	assert.False(t, publicKey.IsZero())
}

func TestWalletValidatePrivateKey(t *testing.T) {
	t.Run("valid private key", func(t *testing.T) {
		wallet, err := NewWallet()
		require.NoError(t, err)

		err = wallet.ValidatePrivateKey()
		require.NoError(t, err)
	})

	t.Run("zero private key", func(t *testing.T) {
		wallet := &Wallet{
			PublicKey:  solana.PublicKey{},
			PrivateKey: solana.PrivateKey{},
		}

		err := wallet.ValidatePrivateKey()
		require.Error(t, err, "invalid private key size")
	})
}
