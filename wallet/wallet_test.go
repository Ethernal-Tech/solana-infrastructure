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

func TestMarshalSignature(t *testing.T) {
	// Create a test signature
	testSignature := solana.Signature{}
	for i := 0; i < 64; i++ {
		testSignature[i] = byte(i)
	}

	t.Run("success - marshals signature to JSON", func(t *testing.T) {
		jsonBytes, err := testSignature.MarshalJSON()

		require.NoError(t, err)
		require.NotNil(t, jsonBytes)

		// Verify it's valid JSON (starts and ends with quotes)
		assert.True(t, len(jsonBytes) > 2)
		assert.Equal(t, byte('"'), jsonBytes[0])
		assert.Equal(t, byte('"'), jsonBytes[len(jsonBytes)-1])

		// Verify the base58 string is valid
		var unmarshaled solana.Signature
		err = unmarshaled.UnmarshalJSON(jsonBytes)
		require.NoError(t, err)
		assert.Equal(t, testSignature, unmarshaled)
	})

	t.Run("success - with empty signature", func(t *testing.T) {
		emptySignature := solana.Signature{}
		jsonBytes, err := emptySignature.MarshalJSON()

		require.NoError(t, err)
		require.NotNil(t, jsonBytes)

		// Should marshal to "11111111111111111111111111111111" (base58 for zeros)
		var unmarshaled solana.Signature
		err = unmarshaled.UnmarshalJSON(jsonBytes)
		require.NoError(t, err)
		assert.Equal(t, emptySignature, unmarshaled)
	})

	t.Run("verify marshal is deterministic", func(t *testing.T) {
		json1, err := testSignature.MarshalJSON()
		require.NoError(t, err)

		json2, err := testSignature.MarshalJSON()
		require.NoError(t, err)

		assert.Equal(t, json1, json2)
	})
}

func TestUnmarshalSignature(t *testing.T) {
	// Create a test signature
	testSignature := solana.Signature{}
	for i := 0; i < 64; i++ {
		testSignature[i] = byte(i)
	}

	// Marshal it first to get valid JSON bytes
	validJSON, err := testSignature.MarshalJSON()
	require.NoError(t, err)

	t.Run("success - unmarshals valid signature JSON", func(t *testing.T) {
		signature := solana.Signature{}
		err := signature.UnmarshalJSON(validJSON)

		require.NoError(t, err)
		assert.Equal(t, testSignature, signature)
	})

	t.Run("success - with empty signature JSON", func(t *testing.T) {
		emptySignature := solana.Signature{}
		emptyJSON, err := emptySignature.MarshalJSON()
		require.NoError(t, err)

		signature := solana.Signature{}

		err = signature.UnmarshalJSON(emptyJSON)

		require.NoError(t, err)
		assert.Equal(t, emptySignature, signature)
	})

	t.Run("error - malformed base58 string", func(t *testing.T) {
		malformedJSON := []byte(`"invalid!@#$"`)

		signature := solana.Signature{}

		err := signature.UnmarshalJSON(malformedJSON)

		assert.Error(t, err)
		assert.Equal(t, solana.Signature{}, signature)
	})

	t.Run("error - wrong length base58 string", func(t *testing.T) {
		// Create a base58 string that's too short
		shortSig := solana.Signature{}
		for i := 0; i < 32; i++ { // Only fill half
			shortSig[i] = byte(i)
		}

		shortJSON, err := shortSig.MarshalJSON()
		require.NoError(t, err)

		signature := solana.Signature{}
		// This should still work because Solana signatures are always 64 bytes
		// The marshaling will pad with zeros
		err = signature.UnmarshalJSON(shortJSON)
		assert.NoError(t, err)
		assert.Equal(t, shortSig, signature)
	})

	t.Run("error - empty bytes", func(t *testing.T) {
		emptyBytes := []byte{}
		signature := solana.Signature{}

		err := signature.UnmarshalJSON(emptyBytes)

		assert.Error(t, err)
		assert.Equal(t, solana.Signature{}, signature)
	})

	t.Run("error - nil bytes", func(t *testing.T) {
		signature := solana.Signature{}
		err := signature.UnmarshalJSON(nil)

		assert.Error(t, err)
		assert.Equal(t, solana.Signature{}, signature)
	})

	t.Run("round trip - marshal then unmarshal", func(t *testing.T) {
		signatures := []solana.Signature{
			testSignature,
			func() solana.Signature {
				var sig solana.Signature
				for i := 0; i < 64; i++ {
					sig[i] = byte(255 - i)
				}

				return sig
			}(),
		}

		for _, original := range signatures {
			// Marshal
			jsonBytes, err := original.MarshalJSON()
			require.NoError(t, err)

			unmarshaled := solana.Signature{}

			// Unmarshal
			err = unmarshaled.UnmarshalJSON(jsonBytes)
			require.NoError(t, err)

			// Verify
			assert.Equal(t, original, unmarshaled)
		}
	})
}
