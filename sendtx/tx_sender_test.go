package sendtx

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Ethernal-Tech/solana-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	binary "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/test-go/testify/assert"
)

func TestNewTxSender(t *testing.T) {
	ctx := context.Background()
	mockProvider := new(MockTxSubmiter)

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	t.Run("valid instruction config", func(t *testing.T) {
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		require.NotNil(t, txSender)
	})

	t.Run("CreateTx - invalid DTO type", func(t *testing.T) {
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			"wrongIxType",
			recentBlockHash,
			interface{}(nil),
		)

		require.Error(t, err, "unsupported transaction type")
	})
}

func TestBridgingRequest(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}
	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: tokenMint,
					},
				},
			},
		}

		expectedSig = mustSignedSignature(t, []byte("bridging-request-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				MinAmountToBridge:  0,
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid treasury address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1_000_000_000),
						TokenMint: tokenMint,
					},
				},
			},
			BridgingFee: 1_000_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    solana.PublicKey{},
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		sig, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "invalid treasury address")
		require.Nil(t, sig)
	})

	t.Run("invalid bridging fee address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1_000_000_000),
						TokenMint: tokenMint,
					},
				},
			},
			BridgingFee: 1_000_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: solana.PublicKey{},
			},
		)

		sig, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)
		require.Error(t, err, "invalid bridging fee address")
		require.Nil(t, sig)
	})

	t.Run("insuficient bridging amount", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1_000_000),
						TokenMint: tokenMint,
					},
				},
			},
			BridgingFee: 1_000_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
				MinAmountToBridge:  1_000_000_000,
				MinFeeForBridging:  1_000_000_000,
			},
		)

		sig, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "amount to bridge is less than the minimum required")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("insuficient bridging fee", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1_000_000_000),
						TokenMint: tokenMint,
					},
				},
			},
			BridgingFee: 1_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
				MinAmountToBridge:  1_000_000_000,
				MinFeeForBridging:  1_000_000_000,
			},
		)

		sig, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "fees do not meet the minimum requirements")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("insuficient operation fee", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1_000_000_000),
						TokenMint: tokenMint,
					},
				},
			},
			BridgingFee:  1_000_000_000,
			OperationFee: 1_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:       treasuryWallet.PublicKey,
				BridgingFeeAddress:    feeWallet.PublicKey,
				MinAmountToBridge:     1_000_000_000,
				MinFeeForBridging:     1_000_000_000,
				MinOperationFeeAmount: 1_000_000_000,
			},
		)

		sig, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "fees do not meet the minimum requirements")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass BridgeTransactionDto instead of BridgeRequestDto
		txDto := BridgeTransactionDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			BatchID:    42,
			Receivers: []BridgingTxReceiver{
				{
					Address: "invalid-address",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.NewWallet().PublicKey().String(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "Expected BridgeRequestDto")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := BridgeRequestDto{
			SenderAddr: "invalid-address",
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.NewWallet().PublicKey().String(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "invalid sender address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid token mint in receiver", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.PublicKey{}.String(), // Zero address - invalid
					},
				},
			},
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				MinAmountToBridge:  0,
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid token mint specified")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("provider error on SendTx", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: tokenMint,
					},
				},
			},
		}

		expectedErr := errors.New("mocked send error")
		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(solana.Signature{}, expectedErr)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
			if key.Equals(senderPrivateKey.PublicKey()) {
				return &senderPrivateKey
			}

			return nil
		})
		require.NoError(t, err)

		_, err = txSender.SendTx(
			ctx,
			tx,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to send")
		mockProvider.AssertExpectations(t)
	})
}

func TestBridgingTransaction(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}
	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		receiverPubKey := solana.NewWallet().PublicKey()
		tokenMint := solana.NewWallet().PublicKey().String()

		bridgeSignerPubKey := solana.NewWallet().PublicKey()
		payloadBytes := []byte{1, 2, 3, 4}

		signaturePairs := map[solana.PublicKey]solana.Signature{
			bridgeSignerPubKey: {1, 2, 3},
		}

		txDto := BridgeTransactionDto{
			SenderAddr:     senderPrivateKey.PublicKey().String(),
			BatchID:        42,
			PayloadBytes:   payloadBytes,
			SignaturePairs: signaturePairs,
			Receivers: []BridgingTxReceiver{
				{
					Address: receiverPubKey.String(),
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: tokenMint,
					},
				},
			},
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-transaction-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of BridgeTransactionDto
		txDto := BridgeRequestDto{
			SenderAddr: solana.NewWallet().PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.NewWallet().PublicKey().String(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "expected BridgeTransactionDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := BridgeTransactionDto{
			SenderAddr: "invalid-address",
			BatchID:    42,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.NewWallet().PublicKey().String(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "invalid receiver address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid receiver address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := BridgeTransactionDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			BatchID:    42,
			Receivers: []BridgingTxReceiver{
				{
					Address: "invalid-address",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.NewWallet().PublicKey().String(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "invalid receiver address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

func TestBridgeVSU(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}
	expectedSig := solana.Signature{}

	//nolint:dupl
	t.Run("success with adding validators", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			SenderAddr:           senderPrivateKey.PublicKey().String(),
			AddingValidatorAddrs: []string{validator1, validator2},
			BatchID:              42,
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-add-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	//nolint:dupl
	t.Run("success with removing validators", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			SenderAddr:             senderPrivateKey.PublicKey().String(),
			RemovingValidatorAddrs: []string{validator1, validator2},
			BatchID:                43,
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-remove-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("success with both adding and removing", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()
		validator3 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			SenderAddr:             senderPrivateKey.PublicKey().String(),
			AddingValidatorAddrs:   []string{validator1, validator2},
			RemovingValidatorAddrs: []string{validator3},
			BatchID:                44,
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-add-remove-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of BridgeVSUDto
		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.NewWallet().PublicKey().String(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected BridgeVSUDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			SenderAddr:           "invalid-address",
			AddingValidatorAddrs: []string{solana.NewWallet().PublicKey().String()},
			BatchID:              1,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid sender address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid adding validator address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			SenderAddr:           senderPrivateKey.PublicKey().String(),
			AddingValidatorAddrs: []string{"invalid-address"},
			BatchID:              1,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid adding validator address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid removing validator address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			SenderAddr:             senderPrivateKey.PublicKey().String(),
			RemovingValidatorAddrs: []string{"invalid-address"},
			BatchID:                1,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid removing validator address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("empty both adding and removing", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-provider-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			BatchID:    1,
		}

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})
}

func TestInitialize(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success with 4 validators", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()
		validator3 := solana.NewWallet().PublicKey().String()
		validator4 := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{validator1, validator2, validator3, validator4},
			LastID:        0,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("success with 10 validators", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		validators := make([]string, 10)

		for i := 0; i < 10; i++ {
			validators[i] = solana.NewWallet().PublicKey().String()
		}

		txDto := InitializeDto{
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    validators,
			LastID:        42,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-provider-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("success with empty validators list", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txDto := InitializeDto{
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{},
			LastID:        0,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-second-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of InitializeDto
		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:    new(big.Int).SetUint64(1000),
						TokenMint: solana.NewWallet().PublicKey().String(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected InitializeDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := InitializeDto{
			AuthorityAddr: "invalid-address",
			Validators:    []string{solana.NewWallet().PublicKey().String()},
			LastID:        0,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid sender address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid validator address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		validValidator := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{validValidator, "invalid-address"},
			LastID:        0,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid validator address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("duplicate validators", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)
		validator := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{validator, validator},
			LastID:        0,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-third-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})
}

func TestRegisterTokenLockUnlock(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txDto := RegisterTokenLockUnlockDto{
			AuthorityAddr:     senderPrivateKey.PublicKey().String(),
			TokenMint:         solana.NewWallet().PublicKey().String(),
			TokenID:           1,
			MinBridgingAmount: 1_000,
		}

		expectedSig = mustSignedSignature(t, []byte("register-token-lock-unlock-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeRegisterTokensLockUnlock,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass InitializeDto instead of RegisterTokenLockUnlockDto
		txDto := InitializeDto{
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{solana.NewWallet().PublicKey().String()},
			LastID:        0,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeRegisterTokensLockUnlock,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected RegisterTokenLockUnlockDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid authority address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := RegisterTokenLockUnlockDto{
			AuthorityAddr:     "invalid-address",
			TokenMint:         solana.NewWallet().PublicKey().String(),
			TokenID:           1,
			MinBridgingAmount: 1_000,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeRegisterTokensLockUnlock,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid authority address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid token mint address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := RegisterTokenLockUnlockDto{
			AuthorityAddr:     senderPrivateKey.PublicKey().String(),
			TokenMint:         "invalid-mint",
			TokenID:           1,
			MinBridgingAmount: 1_000,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeRegisterTokensLockUnlock,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse token mint address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid token mint public key", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Use zero public key which will fail ValidatePublicKey
		txDto := RegisterTokenLockUnlockDto{
			AuthorityAddr:     senderPrivateKey.PublicKey().String(),
			TokenMint:         solana.PublicKey{}.String(),
			TokenID:           1,
			MinBridgingAmount: 1_000,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeRegisterTokensLockUnlock,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid token mint specified")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

func TestUpdateFeeConfig(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		newTreasury := solana.NewWallet().PublicKey().String()
		newRelayer := solana.NewWallet().PublicKey().String()

		txDto := UpdateFeeConfigDto{
			AuthorityAddr:   senderPrivateKey.PublicKey().String(),
			MinOperationFee: 10,
			BridgingFee:     20,

			UpdateTreasury:     true,
			UpdateRelayer:      true,
			NewTreasuryAddress: newTreasury,
			NewRelayerAddress:  newRelayer,
		}

		expectedSig = mustSignedSignature(t, []byte("update-fee-config-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateFeeConfig,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass InitializeDto instead of UpdateFeeConfigDto
		txDto := InitializeDto{
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{solana.NewWallet().PublicKey().String()},
			LastID:        0,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateFeeConfig,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected UpdateFeeConfigDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid authority address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := UpdateFeeConfigDto{
			AuthorityAddr:   "invalid-address",
			MinOperationFee: 10,
			BridgingFee:     20,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateFeeConfig,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid authority address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid new treasury address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := UpdateFeeConfigDto{
			AuthorityAddr:   senderPrivateKey.PublicKey().String(),
			MinOperationFee: 10,
			BridgingFee:     20,

			UpdateTreasury:     true,
			NewTreasuryAddress: "invalid-address",
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateFeeConfig,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid new treasury address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid new relayer address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := UpdateFeeConfigDto{
			AuthorityAddr:   senderPrivateKey.PublicKey().String(),
			MinOperationFee: 10,
			BridgingFee:     20,

			UpdateRelayer:     true,
			NewRelayerAddress: "invalid-address",
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateFeeConfig,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid new relayer address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

func TestSOLTransfer(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txDto := SOLTransferDto{
			SenderPublicKey:   senderPrivateKey.PublicKey().String(),
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
			Amount:            1_000,
		}

		expectedSig = mustSignedSignature(t, []byte("sol-transfer-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSOLTransfer,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of SOLTransferDto
		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSOLTransfer,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected TransferDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txDto := SOLTransferDto{
			SenderPublicKey:   "invalid-sender",
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
			Amount:            1_000,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSOLTransfer,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse sender public key from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid receiver address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		txDto := SOLTransferDto{
			SenderPublicKey:   senderPrivateKey.PublicKey().String(),
			ReceiverPublicKey: "invalid-receiver",
			Amount:            1_000,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSOLTransfer,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse receiver public key from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

func TestSPLTransfer(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)
		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		mint := solana.NewWallet().PublicKey().String()

		txDto := SPLTransferDto{
			SenderPublicKey:   senderWallet.PublicKey.String(),
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
			Amount:            1_000,
			MintTokenAddress:  mint,
			TokenDecimals:     9,
		}

		expectedSig = mustSignedSignature(t, []byte("spl-transfer-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSPLTransfer,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of SPLTransferDto
		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSPLTransfer,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected SPLTransferDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		mint := solana.NewWallet().PublicKey().String()

		txDto := SPLTransferDto{
			SenderPublicKey:   "invalid-sender",
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
			Amount:            1_000,
			MintTokenAddress:  mint,
			TokenDecimals:     9,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSPLTransfer,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse sender public key from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid receiver address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		mint := solana.NewWallet().PublicKey().String()

		txDto := SPLTransferDto{
			SenderPublicKey:   senderWallet.PublicKey.String(),
			ReceiverPublicKey: "invalid-receiver",
			Amount:            1_000,
			MintTokenAddress:  mint,
			TokenDecimals:     9,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSPLTransfer,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse receiver public key from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid mint address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)
		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txDto := SPLTransferDto{
			SenderPublicKey:   senderWallet.PublicKey.String(),
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
			Amount:            1_000,
			MintTokenAddress:  "invalid-mint",
			TokenDecimals:     9,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeSPLTransfer,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse token mint from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

func TestCreateInstruction(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)
		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		mint := solana.NewWallet().PublicKey().String()

		txDto := CreateInstructionDto{
			SenderPublicKey:   senderWallet.PublicKey.String(),
			MintTokenAddress:  mint,
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
		}

		expectedSig = mustSignedSignature(t, []byte("create-instruction-success"))

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionCreateInstruction,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.NotNil(t, tx)

		sig, err := txSender.SendTx(
			ctx,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of CreateInstructionDto
		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionCreateInstruction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected CreateInstructionDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		mint := solana.NewWallet().PublicKey().String()

		txDto := CreateInstructionDto{
			SenderPublicKey:   "invalid-sender",
			MintTokenAddress:  mint,
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionCreateInstruction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse sender public key from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid receiver address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		mint := solana.NewWallet().PublicKey().String()

		txDto := CreateInstructionDto{
			SenderPublicKey:   senderWallet.PublicKey.String(),
			MintTokenAddress:  mint,
			ReceiverPublicKey: "invalid-receiver",
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionCreateInstruction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse receiver public key from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid mint address", func(t *testing.T) {
		mockProvider := new(MockTxSubmiter)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
		)

		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)
		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txDto := CreateInstructionDto{
			SenderPublicKey:   senderWallet.PublicKey.String(),
			MintTokenAddress:  "invalid-mint",
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionCreateInstruction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse token mint from address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

type MockTxSubmiter struct {
	mock.Mock
}

var _ wallet.ITxSubmiter = (*MockTxSubmiter)(nil)

func (m *MockTxSubmiter) SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	args := m.Called(ctx, tx)

	return args.Get(0).(solana.Signature), args.Error(1)
}

func (m *MockTxSubmiter) WaitForSignature(ctx context.Context, sig solana.Signature, commitment rpc.CommitmentType, maxWaitTime time.Duration) error {
	args := m.Called(ctx, sig, commitment, maxWaitTime)

	return args.Error(0)
}

func mustSignedSignature(t *testing.T, payload []byte) solana.Signature {
	t.Helper()

	testWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	signature, err := testWallet.Sign(payload)
	require.NoError(t, err)

	return *signature
}

func TestMarshalTransaction(t *testing.T) {
	txProvider, err := wallet.NewProvider(rpc.LocalNet_RPC)
	require.NoError(t, err)

	chainConfig := &ChainConfig{
		TreasuryAddress:    solana.MustPublicKeyFromBase58("AXXWYCH6PNm6AGjaasPG1maarfQvRedSw18wj91Nem1F"),
		BridgingFeeAddress: solana.MustPublicKeyFromBase58("7d5xBAeX92qPugMB5vixR1cy3wpRCxKE7ckShZaJbPPL"),
	}

	txSender := NewTxSender(txProvider, chainConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	recentBlockHash := solana.Hash{123}
	payloadBytes := []byte{1, 2, 3, 4}
	signaturePairs := map[solana.PublicKey]solana.Signature{
		solana.MustPublicKeyFromBase58("BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa"): {1, 2, 3},
	}

	txDto := BridgeTransactionDto{
		SenderAddr: "BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa",
		Receivers: []BridgingTxReceiver{
			{
				Address: "BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa",
				TokenAmount: wallet.TokenAmount{
					Amount:    new(big.Int).SetUint64(1000),
					TokenMint: "BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa",
				},
			},
		},
		BatchID:        1,
		PayloadBytes:   payloadBytes,
		SignaturePairs: signaturePairs,
	}

	tx, err := txSender.CreateTx(
		ctx,
		solana.MustPublicKeyFromBase58("BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa"),
		InstructionTypeBridgeTransaction,
		recentBlockHash,
		txDto,
	)
	require.NoError(t, err)
	require.NotNil(t, tx)

	rawTx, err := wallet.MarshalTransaction(tx)
	require.NoError(t, err)
	require.NotNil(t, rawTx)

	unmarshaledTx, err := wallet.UnmarshalTransaction(rawTx)
	require.NoError(t, err)
	require.NotNil(t, unmarshaledTx)
	require.Equal(t, tx.Message.RecentBlockhash, unmarshaledTx.Message.RecentBlockhash)
	require.Equal(t, tx.Message.Instructions, unmarshaledTx.Message.Instructions)

	require.Len(t, tx.Message.Instructions, 2)

	instrData := tx.Message.Instructions[1].Data
	// skip the 8-byte instruction discriminator
	dec := binary.NewBorshDecoder(instrData[8:])

	var transfers []skyline_program.TransferItem
	err = dec.Decode(&transfers)
	require.NoError(t, err)

	var mints []solana.PublicKey
	err = dec.Decode(&mints)
	require.NoError(t, err)

	var batchID uint64
	err = dec.Decode(&batchID)
	require.NoError(t, err)

	require.Len(t, transfers, 1)
	require.Equal(t, solana.MustPublicKeyFromBase58("BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa"), transfers[0].Recipient)
	require.Equal(t, uint8(0), transfers[0].MintIndex)
	require.Equal(t, uint64(1000), transfers[0].Amount)

	require.Len(t, mints, 1)
	require.Equal(t, solana.MustPublicKeyFromBase58("BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa"), mints[0])

	require.Equal(t, uint64(1), batchID)
}
