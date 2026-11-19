package sendtx

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/test-go/testify/assert"
)

func TestNewTxSender(t *testing.T) {
	ctx := context.Background()
	mockProvider := new(wallet.MockTxProvider)

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	t.Run("valid instruction config", func(t *testing.T) {
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		require.NotNil(t, txSender)
	})

	t.Run("CreateTx - invalid DTO type", func(t *testing.T) {
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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

	recentBlockHash := solana.Hash{}
	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
					},
				},
			},
		}

		expectedSig = mustSignedSignature(t, []byte("bridging-request-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				MinAmountToBridge: 0,
				TreasuryAddress:   treasuryWallet.PublicKey,
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

	t.Run("requires bridge program ID", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		txDto := BridgeRequestDto{
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
					},
				},
			},
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				MinAmountToBridge:     0,
				TreasuryAddress:       treasuryWallet.PublicKey,
				MinFeeForBridging:     0,
				MinOperationFeeAmount: 0,
			},
		)

		_, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "bridge program ID is required")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid treasury address", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1_000_000_000,
						TokenID: 1,
					},
				},
			},
			BridgingFee: 1_000_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: solana.PublicKey{},
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

	t.Run("insuficient bridging amount", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1_000_000,
						TokenID: 1,
					},
				},
			},
			BridgingFee: 1_000_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:   treasuryWallet.PublicKey,
				MinAmountToBridge: 1_000_000_000,
				MinFeeForBridging: 1_000_000_000,
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
		mockProvider := new(wallet.MockTxProvider)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1_000_000_000,
						TokenID: 1,
					},
				},
			},
			BridgingFee: 1_000_000,
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:   treasuryWallet.PublicKey,
				MinAmountToBridge: 1_000_000_000,
				MinFeeForBridging: 1_000_000_000,
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
		mockProvider := new(wallet.MockTxProvider)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1_000_000_000,
						TokenID: 1,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass BridgeTransactionDto instead of BridgeRequestDto
		txDto := BridgeTransactionDto{
			ProgramID:    skyline_program.ProgramID,
			SenderAddr:   senderPrivateKey.PublicKey().String(),
			PayloadBytes: []byte{1, 2, 3, 4},
			SignaturePairs: map[solana.PublicKey]solana.Signature{
				solana.NewWallet().PublicKey(): {1, 2, 3},
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: "invalid-address",
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
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
		mockProvider := new(wallet.MockTxProvider)
		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
					},
				},
			},
		}

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				MinAmountToBridge: 0,
				TreasuryAddress:   treasuryWallet.PublicKey,
			},
		)

		mockBridgeTokenRegistry(t, mockProvider)

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
		mockProvider := new(wallet.MockTxProvider)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
					},
				},
			},
		}

		expectedErr := errors.New("mocked send error")
		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(solana.Signature{}, expectedErr)

		mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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

	recentBlockHash := solana.Hash{}
	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		bridgeSignerPubKey := solana.NewWallet().PublicKey()
		payloadBytes := []byte{1, 2, 3, 4}

		signaturePairs := map[solana.PublicKey]solana.Signature{
			bridgeSignerPubKey: {1, 2, 3},
		}

		txDto := BridgeTransactionDto{
			ProgramID:      skyline_program.ProgramID,
			SenderAddr:     senderPrivateKey.PublicKey().String(),
			PayloadBytes:   payloadBytes,
			SignaturePairs: signaturePairs,
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-transaction-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of BridgeTransactionDto
		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: solana.NewWallet().PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := BridgeTransactionDto{
			ProgramID:    skyline_program.ProgramID,
			SenderAddr:   "invalid-address",
			PayloadBytes: []byte{1, 2, 3, 4},
			SignaturePairs: map[solana.PublicKey]solana.Signature{
				solana.NewWallet().PublicKey(): {1, 2, 3},
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
		mockProvider := new(wallet.MockTxProvider)
		mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		offCurveReceiver, _, err := solana.FindProgramAddress(
			[][]byte{[]byte("invalid-receiver")},
			skyline_program.ProgramID,
		)
		require.NoError(t, err)

		txDto := BridgeTransactionDto{
			ProgramID:    skyline_program.ProgramID,
			SenderAddr:   senderPrivateKey.PublicKey().String(),
			PayloadBytes: []byte{1, 2, 3, 4},
			SignaturePairs: map[solana.PublicKey]solana.Signature{
				solana.NewWallet().PublicKey(): {1, 2, 3},
			},
			Receivers: []PayloadReceiver{
				{
					Address: offCurveReceiver,
					TokenAmount: wallet.TokenAmount{
						TokenID: 1,
						Amount:  1000,
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

	recentBlockHash := solana.Hash{}
	expectedSig := solana.Signature{}

	//nolint:dupl
	t.Run("success with adding validators", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			ProgramID:            skyline_program.ProgramID,
			SenderAddr:           senderPrivateKey.PublicKey().String(),
			AddingValidatorAddrs: []string{validator1, validator2},
			BatchID:              42,
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-add-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			ProgramID:              skyline_program.ProgramID,
			SenderAddr:             senderPrivateKey.PublicKey().String(),
			RemovingValidatorAddrs: []string{validator1, validator2},
			BatchID:                43,
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-remove-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()
		validator3 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			ProgramID:              skyline_program.ProgramID,
			SenderAddr:             senderPrivateKey.PublicKey().String(),
			AddingValidatorAddrs:   []string{validator1, validator2},
			RemovingValidatorAddrs: []string{validator3},
			BatchID:                44,
		}

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-add-remove-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of BridgeVSUDto
		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			ProgramID:            skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			ProgramID:            skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			ProgramID:              skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		expectedSig = mustSignedSignature(t, []byte("bridge-vsu-provider-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := BridgeVSUDto{
			ProgramID:  skyline_program.ProgramID,
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

func TestHotWalletIncrement(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}
	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)
		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := HotWalletIncrementDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			TokenMint:  tokenMint,
			Amount:     1_000,
		}

		expectedSig = mustSignedSignature(t, []byte("hot-wallet-increment-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeHotWalletIncrement,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeHotWalletIncrement,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected HotWalletIncrementDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := HotWalletIncrementDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: "invalid-address",
			TokenMint:  solana.NewWallet().PublicKey().String(),
			Amount:     1_000,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeHotWalletIncrement,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid sender address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid token mint", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := HotWalletIncrementDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			TokenMint:  "invalid-mint",
			Amount:     1_000,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeHotWalletIncrement,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse token mint address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("zero amount", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := HotWalletIncrementDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			TokenMint:  solana.NewWallet().PublicKey().String(),
			Amount:     0,
		}

		_, err = txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeHotWalletIncrement,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "amount must be greater than zero")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

func TestInitialize(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success with 4 validators", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()
		validator3 := solana.NewWallet().PublicKey().String()
		validator4 := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{validator1, validator2, validator3, validator4},
			LastID:        0,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		validators := make([]string, 10)

		for i := 0; i < 10; i++ {
			validators[i] = solana.NewWallet().PublicKey().String()
		}

		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    validators,
			LastID:        42,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-provider-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{},
			LastID:        0,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-second-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of InitializeDto
		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
			SenderAddr: senderPrivateKey.PublicKey().String(),
			DstChainID: common.ChainIDPrime,
			Receivers: []BridgingTxReceiver{
				{
					Address: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:  1000,
						TokenID: 1,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		validValidator := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)
		validator := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{validator, validator},
			LastID:        0,
		}

		expectedSig = mustSignedSignature(t, []byte("initialize-third-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		txDto := RegisterTokenLockUnlockDto{
			ProgramID:         skyline_program.ProgramID,
			AuthorityAddr:     senderPrivateKey.PublicKey().String(),
			TokenMint:         solana.NewWallet().PublicKey().String(),
			TokenID:           1,
			MinBridgingAmount: 1_000,
		}

		expectedSig = mustSignedSignature(t, []byte("register-token-lock-unlock-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass InitializeDto instead of RegisterTokenLockUnlockDto
		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := RegisterTokenLockUnlockDto{
			ProgramID:         skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := RegisterTokenLockUnlockDto{
			ProgramID:         skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Use zero public key which will fail ValidatePublicKey
		txDto := RegisterTokenLockUnlockDto{
			ProgramID:         skyline_program.ProgramID,
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

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		newTreasury := solana.NewWallet().PublicKey().String()

		txDto := UpdateFeeConfigDto{
			ProgramID:       skyline_program.ProgramID,
			AuthorityAddr:   senderPrivateKey.PublicKey().String(),
			MinOperationFee: 10,
			BridgingFee:     20,

			UpdateTreasury:     true,
			NewTreasuryAddress: newTreasury,
		}

		expectedSig = mustSignedSignature(t, []byte("update-fee-config-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass InitializeDto instead of UpdateFeeConfigDto
		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := UpdateFeeConfigDto{
			ProgramID:       skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := UpdateFeeConfigDto{
			ProgramID:       skyline_program.ProgramID,
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
}

func TestUpdateProgramVersion(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		txDto := UpdateProgramVersionDto{
			ProgramID:     skyline_program.ProgramID,
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			VersionString: "1.2.3",
		}

		expectedSig = mustSignedSignature(t, []byte("update-program-version-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		tx, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateProgramVersion,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := InitializeDto{
			ProgramID:     skyline_program.ProgramID,
			AuthorityAddr: senderPrivateKey.PublicKey().String(),
			Validators:    []string{solana.NewWallet().PublicKey().String()},
			LastID:        0,
		}

		_, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateProgramVersion,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected UpdateProgramVersionDto")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})

	t.Run("invalid authority address", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		txDto := UpdateProgramVersionDto{
			ProgramID:     skyline_program.ProgramID,
			AuthorityAddr: "invalid-address",
			VersionString: "1.0.0",
		}

		_, err := txSender.CreateTx(
			ctx,
			senderPrivateKey.PublicKey(),
			InstructionTypeUpdateProgramVersion,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid authority address")
		mockProvider.AssertNotCalled(t, "SendTransaction")
	})
}

func TestSOLTransfer(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		receiverWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txDto := SOLTransferDto{
			SenderPublicKey:   senderPrivateKey.PublicKey().String(),
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
			Amount:            1_000,
		}

		expectedSig = mustSignedSignature(t, []byte("sol-transfer-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of SOLTransferDto
		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

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

		expectedSig = mustSignedSignature(t, []byte("spl-transfer-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of SPLTransferDto
		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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

	recentBlockHash := solana.Hash{}

	expectedSig := solana.Signature{}

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

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

		expectedSig = mustSignedSignature(t, []byte("create-instruction-success"), senderPrivateKey)

		mockProvider.On("SendTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Transaction"),
		).Return(expectedSig, nil)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
			},
		)

		// Pass BridgeRequestDto instead of CreateInstructionDto
		txDto := BridgeRequestDto{
			ProgramID:  skyline_program.ProgramID,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress: treasuryWallet.PublicKey,
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

func TestTxSender_GetProgramConfig(t *testing.T) {
	ctx := context.Background()

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	chainConfig := &ChainConfig{
		TreasuryAddress: treasuryWallet.PublicKey,
	}

	programID := skyline_program.ProgramID
	pda, _, err := solana.FindProgramAddress([][]byte{skyline_program.PROGRAM_CONFIG_SEED}, programID)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		auth := solana.NewWallet().PublicKey()
		cfgwant := skyline_program.ProgramConfig{
			VersionString: "1.23.45",
			DeployedAt:    42,
			Authority:     auth,
		}
		body, err := cfgwant.Marshal()
		require.NoError(t, err)

		raw := append(append([]byte(nil), skyline_program.Account_ProgramConfig[:]...), body...)

		mockProvider.On("GetAccountInfo", mock.Anything, pda).Return(&rpc.GetAccountInfoResult{
			Value: &rpc.Account{Data: rpc.DataBytesOrJSONFromBytes(raw)},
		}, nil).Once()

		txSender := NewTxSender(mockProvider, chainConfig)
		got, err := txSender.GetProgramConfig(ctx, programID)
		require.NoError(t, err)
		require.Equal(t, cfgwant.DeployedAt, got.DeployedAt)
		require.Equal(t, cfgwant.Authority, got.Authority)
	})
}

func TestTxSender_GetAllRegisteredTokens(t *testing.T) {
	ctx := context.Background()

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	chainConfig := &ChainConfig{
		TreasuryAddress: treasuryWallet.PublicKey,
	}

	programID := skyline_program.ProgramID

	t.Run("success", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)

		first := skyline_program.TokenRegistry{
			TokenId:           1,
			Mint:              solana.NewWallet().PublicKey(),
			IsLockUnlock:      true,
			MinBridgingAmount: 10,
			Bump:              250,
		}
		second := skyline_program.TokenRegistry{
			TokenId:           2,
			Mint:              solana.NewWallet().PublicKey(),
			IsLockUnlock:      false,
			MinBridgingAmount: 20,
			Bump:              251,
		}

		firstBody, err := first.Marshal()
		require.NoError(t, err)
		secondBody, err := second.Marshal()
		require.NoError(t, err)

		firstRaw := append(append([]byte(nil), skyline_program.Account_TokenRegistry[:]...), firstBody...)
		secondRaw := append(append([]byte(nil), skyline_program.Account_TokenRegistry[:]...), secondBody...)

		mockProvider.On("GetProgramAccounts", mock.Anything, programID, mock.AnythingOfType("*rpc.GetProgramAccountsOpts")).
			Return(rpc.GetProgramAccountsResult{
				{
					Pubkey:  solana.NewWallet().PublicKey(),
					Account: &rpc.Account{Data: rpc.DataBytesOrJSONFromBytes(firstRaw)},
				},
				{
					Pubkey:  solana.NewWallet().PublicKey(),
					Account: &rpc.Account{Data: rpc.DataBytesOrJSONFromBytes(secondRaw)},
				},
			}, nil).Once()

		txSender := NewTxSender(mockProvider, chainConfig)
		got, err := txSender.GetAllRegisteredTokens(ctx, programID)
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Equal(t, first.TokenId, got[first.TokenId].TokenId)
		require.Equal(t, first.Mint, got[first.TokenId].Mint)
		require.Equal(t, second.TokenId, got[second.TokenId].TokenId)
		require.Equal(t, second.IsLockUnlock, got[second.TokenId].IsLockUnlock)
	})

	t.Run("provider error", func(t *testing.T) {
		mockProvider := new(wallet.MockTxProvider)
		mockProvider.On("GetProgramAccounts", mock.Anything, programID, mock.AnythingOfType("*rpc.GetProgramAccountsOpts")).
			Return(rpc.GetProgramAccountsResult(nil), errors.New("rpc unavailable")).Once()

		txSender := NewTxSender(mockProvider, chainConfig)
		_, err := txSender.GetAllRegisteredTokens(ctx, programID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "get token registry accounts")
	})
}

func mustSignedSignature(t *testing.T, payload []byte, signer solana.PrivateKey) solana.Signature {
	t.Helper()

	signature, err := signer.Sign(payload)
	require.NoError(t, err)

	return signature
}

func mockBridgeTokenRegistry(
	t *testing.T,
	mockProvider *wallet.MockTxProvider,
	regs ...skyline_program.TokenRegistry,
) {
	t.Helper()

	results := make(rpc.GetProgramAccountsResult, 0, len(regs))

	for _, reg := range regs {
		body, err := reg.Marshal()
		require.NoError(t, err)

		raw := append(append([]byte(nil), skyline_program.Account_TokenRegistry[:]...), body...)
		results = append(results, &rpc.KeyedAccount{
			Pubkey:  solana.NewWallet().PublicKey(),
			Account: &rpc.Account{Data: rpc.DataBytesOrJSONFromBytes(raw)},
		})
	}

	mockProvider.On(
		"GetProgramAccounts",
		mock.Anything,
		skyline_program.ProgramID,
		mock.AnythingOfType("*rpc.GetProgramAccountsOpts"),
	).Return(results, nil)
}

func defaultBridgeTokenRegistry(tokenID uint16) skyline_program.TokenRegistry {
	return skyline_program.TokenRegistry{
		TokenId:           tokenID,
		Mint:              solana.NewWallet().PublicKey(),
		IsLockUnlock:      true,
		MinBridgingAmount: 10,
		Bump:              250,
	}
}

func TestMarshalTransaction(t *testing.T) {
	mockProvider := new(wallet.MockTxProvider)
	mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

	chainConfig := &ChainConfig{
		TreasuryAddress: solana.MustPublicKeyFromBase58("AXXWYCH6PNm6AGjaasPG1maarfQvRedSw18wj91Nem1F"),
	}

	txSender := NewTxSender(mockProvider, chainConfig)

	ctx := context.Background()

	recentBlockHash := solana.Hash{123}
	payloadBytes := []byte{1, 2, 3, 4}
	signaturePairs := map[solana.PublicKey]solana.Signature{
		solana.MustPublicKeyFromBase58("BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa"): {1, 2, 3},
	}

	txDto := BridgeTransactionDto{
		ProgramID:      skyline_program.ProgramID,
		SenderAddr:     "BU13B5RBqMRLvzYKaC3nTE7C3Vso3RNXzVnMVUGMgRfa",
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
}

func TestBuildBatchedEd25519Instruction(t *testing.T) {
	signaturePairs := func(n int) map[solana.PublicKey]solana.Signature {
		pairs := make(map[solana.PublicKey]solana.Signature, n)

		for i := 0; i < n; i++ {
			var pubkey solana.PublicKey

			pubkey[0], pubkey[1] = byte(i), byte(i>>8)
			pairs[pubkey] = solana.Signature{}
		}

		return pairs
	}

	txSnd := &TxSender{}
	payload := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	t.Run("max signature pairs", func(t *testing.T) {
		ix, err := txSnd.buildBatchedEd25519Instruction(signaturePairs(255), payload)
		require.NoError(t, err)

		data, err := ix.Data()
		require.NoError(t, err)
		require.Equal(t, byte(255), data[0])
	})

	t.Run("too many signature pairs", func(t *testing.T) {
		_, err := txSnd.buildBatchedEd25519Instruction(signaturePairs(256), payload)
		require.ErrorContains(t, err, "too many signature pairs: 256")
	})
}

func TestSetAuthority(t *testing.T) {
	ctx := context.Background()

	newSender := func() *TxSender {
		return NewTxSender(new(wallet.MockTxProvider), &ChainConfig{})
	}

	randomKey := func(t *testing.T) solana.PublicKey {
		t.Helper()

		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		return key.PublicKey()
	}

	pdaFor := func(t *testing.T, programID solana.PublicKey, seed []byte) solana.PublicKey {
		t.Helper()

		pda, _, err := solana.FindProgramAddress([][]byte{seed}, programID)
		require.NoError(t, err)

		return pda
	}

	t.Run("success", func(t *testing.T) {
		programID := randomKey(t)
		feePayer := randomKey(t)
		authority := randomKey(t)
		newAuthority := randomKey(t)

		tx, err := newSender().CreateTx(ctx, feePayer, InstructionTypeSetAuthority, solana.Hash{}, SetAuthorityDto{
			ProgramID:        programID,
			AuthorityAddr:    authority.String(),
			NewAuthorityAddr: newAuthority.String(),
		})
		require.NoError(t, err)
		require.Len(t, tx.Message.Instructions, 1)

		ix := tx.Message.Instructions[0]
		keys := tx.Message.AccountKeys

		require.Equal(t, programID, keys[ix.ProgramIDIndex])
		require.Len(t, ix.Data, 40)
		require.Equal(t, skyline_program.Instruction_SetAuthority[:], []byte(ix.Data[:8]))
		require.Equal(t, newAuthority.Bytes(), []byte(ix.Data[8:]))

		require.Len(t, ix.Accounts, 4)

		feeConfig := pdaFor(t, programID, skyline_program.FEE_CONFIG_SEED)
		programConfig := pdaFor(t, programID, skyline_program.PROGRAM_CONFIG_SEED)

		require.Equal(t, authority, keys[ix.Accounts[0]])
		require.Equal(t, feeConfig, keys[ix.Accounts[1]])
		require.Equal(t, programConfig, keys[ix.Accounts[2]])
		require.Equal(t, newAuthority, keys[ix.Accounts[3]])

		require.True(t, tx.Message.IsSigner(authority))
		require.False(t, isWritable(t, tx, authority))
		require.True(t, tx.Message.IsSigner(newAuthority))
		require.False(t, isWritable(t, tx, newAuthority))
		require.True(t, isWritable(t, tx, feeConfig))
		require.True(t, isWritable(t, tx, programConfig))
		require.False(t, tx.Message.IsSigner(feeConfig))
		require.False(t, tx.Message.IsSigner(programConfig))
	})

	t.Run("invalid addresses", func(t *testing.T) {
		valid := randomKey(t).String()

		shortB58 := base58Of(t, 31)
		longB58 := base58Of(t, 33)

		cases := []struct {
			name      string
			authority string
			newAuth   string
			contains  string
		}{
			{"option-like authority", "--final", valid, "invalid authority address"},
			{"option-like new authority", valid, "--final", "invalid new authority address"},
			{"empty authority", "", valid, "invalid authority address"},
			{"empty new authority", valid, "", "invalid new authority address"},
			{"31 byte authority", shortB58, valid, "invalid authority address"},
			{"33 byte new authority", valid, longB58, "invalid new authority address"},
			{"whitespace authority", " " + valid + " ", valid, "invalid authority address"},
			{"whitespace new authority", valid, valid[:10] + " " + valid[10:], "invalid new authority address"},
		}

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				_, err := newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
					SetAuthorityDto{
						ProgramID:        randomKey(t),
						AuthorityAddr:    c.authority,
						NewAuthorityAddr: c.newAuth,
					})
				require.Error(t, err)
				require.Contains(t, err.Error(), c.contains)
			})
		}
	})

	t.Run("default", func(t *testing.T) {
		_, err := newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
			SetAuthorityDto{
				ProgramID:        randomKey(t),
				AuthorityAddr:    randomKey(t).String(),
				NewAuthorityAddr: solana.PublicKey{}.String(),
			})
		require.Error(t, err)
		require.Contains(t, err.Error(), "new authority")
	})

	t.Run("missing program id", func(t *testing.T) {
		_, err := newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
			SetAuthorityDto{
				AuthorityAddr:    randomKey(t).String(),
				NewAuthorityAddr: randomKey(t).String(),
			})
		require.Error(t, err)
		require.Contains(t, err.Error(), "bridge program ID is required")
	})

	t.Run("wrong dto", func(t *testing.T) {
		_, err := newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
			UpdateProgramVersionDto{ProgramID: randomKey(t)})
		require.Error(t, err)
		require.Contains(t, err.Error(), "expected SetAuthorityDto")
	})

	t.Run("legacy message", func(t *testing.T) {
		tx, err := newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
			SetAuthorityDto{
				ProgramID:        randomKey(t),
				AuthorityAddr:    randomKey(t).String(),
				NewAuthorityAddr: randomKey(t).String(),
			})
		require.NoError(t, err)
		require.Equal(t, solana.MessageVersionLegacy, tx.Message.GetVersion())
	})

	t.Run("off-curve addresses", func(t *testing.T) {
		programID := randomKey(t)
		offCurve := pdaFor(t, programID, []byte("off-curve"))
		require.False(t, offCurve.IsOnCurve())

		_, err := newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
			SetAuthorityDto{
				ProgramID:        programID,
				AuthorityAddr:    offCurve.String(),
				NewAuthorityAddr: randomKey(t).String(),
			})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid authority address")

		// the new authority co-signs, so a PDA (which cannot sign) is rejected
		_, err = newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
			SetAuthorityDto{
				ProgramID:        programID,
				AuthorityAddr:    randomKey(t).String(),
				NewAuthorityAddr: offCurve.String(),
			})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid new authority address")
	})

	t.Run("fee payer is new authority", func(t *testing.T) {
		newAuthority := randomKey(t)

		tx, err := newSender().CreateTx(ctx, newAuthority, InstructionTypeSetAuthority, solana.Hash{},
			SetAuthorityDto{
				ProgramID:        randomKey(t),
				AuthorityAddr:    randomKey(t).String(),
				NewAuthorityAddr: newAuthority.String(),
			})
		require.NoError(t, err)
		require.Equal(t, 2, int(tx.Message.Header.NumRequiredSignatures))
		require.Equal(t, newAuthority, tx.Message.AccountKeys[0])
		require.True(t, tx.Message.IsSigner(newAuthority))
	})

	t.Run("same authority", func(t *testing.T) {
		same := randomKey(t).String()

		_, err := newSender().CreateTx(ctx, randomKey(t), InstructionTypeSetAuthority, solana.Hash{},
			SetAuthorityDto{ProgramID: randomKey(t), AuthorityAddr: same, NewAuthorityAddr: same})
		require.Error(t, err)
		require.Contains(t, err.Error(), "differ")
	})

	t.Run("sender reuse", func(t *testing.T) {
		sender := newSender()
		authority := randomKey(t)

		for i := 0; i < 2; i++ {
			programID := randomKey(t)

			tx, err := sender.CreateTx(ctx, authority, InstructionTypeSetAuthority, solana.Hash{},
				SetAuthorityDto{
					ProgramID:        programID,
					AuthorityAddr:    authority.String(),
					NewAuthorityAddr: randomKey(t).String(),
				})
			require.NoError(t, err)

			ix := tx.Message.Instructions[0]
			require.Equal(t, programID, tx.Message.AccountKeys[ix.ProgramIDIndex])
			require.Equal(t, pdaFor(t, programID, skyline_program.FEE_CONFIG_SEED),
				tx.Message.AccountKeys[ix.Accounts[1]])
			require.Equal(t, pdaFor(t, programID, skyline_program.PROGRAM_CONFIG_SEED),
				tx.Message.AccountKeys[ix.Accounts[2]])
		}
	})
}

// base58Of returns the base58 encoding of n random bytes.
func base58Of(t *testing.T, n int) string {
	t.Helper()

	buf := make([]byte, n)
	_, err := rand.Read(buf)
	require.NoError(t, err)

	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

	// force a non-zero leading byte so the length is not shortened by leading '1's
	buf[0] |= 0x80

	num := new(big.Int).SetBytes(buf)
	base := big.NewInt(58)
	mod := new(big.Int)

	var out []byte

	for num.Sign() > 0 {
		num.DivMod(num, base, mod)
		out = append([]byte{alphabet[mod.Int64()]}, out...)
	}

	return string(out)
}

func isWritable(t *testing.T, tx *solana.Transaction, key solana.PublicKey) bool {
	t.Helper()

	writable, err := tx.Message.IsWritable(key)
	require.NoError(t, err)

	return writable
}
