package sendtx

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/test-go/testify/assert"
)

func TestNewTxSender(t *testing.T) {
	ctx := context.Background()
	mockProvider := new(MockSenderTxProvider)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	programKeyPair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	t.Run("valid instruction config", func(t *testing.T) {
		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)

		require.NoError(t, err)
		require.NotNil(t, txSender)
	})

	t.Run("missing vault PDA", func(t *testing.T) {
		mockProvider := new(MockSenderTxProvider)

		instructionConfig := InstructionConfig{
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)

		require.Error(t, err, "vaultPDA")
		require.Nil(t, txSender)
	})

	t.Run("missing validator set PDA", func(t *testing.T) {
		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)

		require.Error(t, err, "validatorSetPDA")
		require.Nil(t, txSender)
	})

	t.Run("CreateTx - invalid DTO type", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			InstructionConfig{
				vaultPDA:                           solana.NewWallet().PublicKey(),
				validatorSetPDA:                    solana.NewWallet().PublicKey(),
				tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
				tokenProgramID:                     solana.TokenProgramID,
				systemProgramID:                    solana.SystemProgramID,
				splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
				programKeyPair:                     programKeyPair,
			},
		)
		require.NoError(t, err)

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			"wrongIxType",
			recentBlockHash,
			interface{}(nil),
		)

		require.Error(t, err, "unsupported transaction type")
	})

	t.Run("invalid treasury address", func(t *testing.T) {
		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    solana.PublicKey{},
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)

		require.Error(t, err, "invalid treasury address")
		require.Nil(t, txSender)
	})

	t.Run("invalid bridging fee address", func(t *testing.T) {
		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: solana.PublicKey{},
			},
			instructionConfig,
		)

		require.Error(t, err, "invalid bridging fee address")
		require.Nil(t, txSender)
	})
}

func TestBridgingRequest(t *testing.T) {
	ctx := context.Background()
	mockProvider := new(MockSenderTxProvider)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	programKeyPair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	instructionConfig := InstructionConfig{
		vaultPDA:                           solana.NewWallet().PublicKey(),
		validatorSetPDA:                    solana.NewWallet().PublicKey(),
		tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
		tokenProgramID:                     solana.TokenProgramID,
		systemProgramID:                    solana.SystemProgramID,
		splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
		programKeyPair:                     programKeyPair,
	}

	recentBlockHash := solana.Hash{}

	t.Run("success", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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

		expectedTx := &solana.Transaction{}
		expectedSig := solana.Signature{1, 2, 3}

		// Mock CreateIxTransaction
		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		// Mock ExecuteTransaction for SendTx
		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				MinAmountToBridge:  0,
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		// CreateTx
		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		// SendTx
		sig, err := txSender.SendTx(
			ctx,
			*senderWallet,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	//nolint:dupl
	t.Run("insuficient bridging amount", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
				MinAmountToBridge:  1_000_000_000,
				MinFeeForBridging:  1_000_000_000,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "amount to bridge is less than the minimum required")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	//nolint:dupl
	t.Run("insuficient bridging fee", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
				MinAmountToBridge:  1_000_000_000,
				MinFeeForBridging:  1_000_000_000,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "fees do not meet the minimum requirements")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("insuficient operation fee", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:       treasuryWallet.PublicKey,
				BridgingFeeAddress:    feeWallet.PublicKey,
				MinAmountToBridge:     1_000_000_000,
				MinFeeForBridging:     1_000_000_000,
				MinOperationFeeAmount: 1_000_000_000,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "fees do not meet the minimum requirements")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		// Pass BridgeTransactionDto instead of BridgeRequestDto
		txDto := BridgeTransactionDto{
			SenderAddr: senderWallet.PublicKey.String(),
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
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "Expected BridgeRequestDto")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		txDto := BridgeRequestDto{
			SenderAddr: "invalid-address",
			DstChainID: 1,
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
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "invalid sender address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("provider error", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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

		expectedErr := errors.New("mocked create error")
		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(nil, expectedErr)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "failed to send bridging request transaction")
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid token mint in receiver", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				MinAmountToBridge:  0,
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgingRequest,
			recentBlockHash,
			txDto,
		)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid token mint specified")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})
}

func TestBridgingTransaction(t *testing.T) {
	ctx := context.Background()
	mockProvider := new(MockSenderTxProvider)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	programKeyPair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	instructionConfig := InstructionConfig{
		vaultPDA:                           solana.NewWallet().PublicKey(),
		validatorSetPDA:                    solana.NewWallet().PublicKey(),
		tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
		tokenProgramID:                     solana.TokenProgramID,
		systemProgramID:                    solana.SystemProgramID,
		splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
		programKeyPair:                     programKeyPair,
	}

	recentBlockHash := solana.Hash{}

	t.Run("success", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		receiverPubKey := solana.NewWallet().PublicKey()
		tokenMint := solana.NewWallet().PublicKey().String()

		txDto := BridgeTransactionDto{
			SenderAddr: senderWallet.PublicKey.String(),
			BatchID:    42,
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

		expectedTx := &solana.Transaction{}
		expectedSig := solana.Signature{4, 5, 6}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		// Pass BridgeRequestDto instead of BridgeTransactionDto
		txDto := BridgeRequestDto{
			SenderAddr: solana.NewWallet().PublicKey().String(),
			DstChainID: 1,
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
			*senderWallet,
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "expected BridgeTransactionDto")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

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
			*senderWallet,
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "invalid receiver address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid receiver address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		txDto := BridgeTransactionDto{
			SenderAddr: senderWallet.PublicKey.String(),
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
			*senderWallet,
			InstructionTypeBridgeTransaction,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err, "invalid receiver address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})
}

func TestBridgeVSU(t *testing.T) {
	ctx := context.Background()
	mockProvider := new(MockSenderTxProvider)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	programKeyPair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	instructionConfig := InstructionConfig{
		vaultPDA:                           solana.NewWallet().PublicKey(),
		validatorSetPDA:                    solana.NewWallet().PublicKey(),
		tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
		tokenProgramID:                     solana.TokenProgramID,
		systemProgramID:                    solana.SystemProgramID,
		splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
		programKeyPair:                     programKeyPair,
	}

	//nolint:dupl
	t.Run("success with adding validators", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			SenderAddr:           senderWallet.PublicKey.String(),
			AddingValidatorAddrs: []string{validator1, validator2},
			BatchID:              42,
		}

		expectedTx := &solana.Transaction{}
		expectedSig := solana.Signature{4, 5, 6}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	//nolint:dupl
	t.Run("success with removing validators", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			SenderAddr:             senderWallet.PublicKey.String(),
			RemovingValidatorAddrs: []string{validator1, validator2},
			BatchID:                43,
		}

		expectedTx := &solana.Transaction{}
		expectedSig := solana.Signature{4, 5, 6}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("success with both adding and removing", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()
		validator3 := solana.NewWallet().PublicKey().String()

		txDto := BridgeVSUDto{
			SenderAddr:             senderWallet.PublicKey.String(),
			AddingValidatorAddrs:   []string{validator1, validator2},
			RemovingValidatorAddrs: []string{validator3},
			BatchID:                44,
		}

		expectedTx := &solana.Transaction{}
		expectedSig := solana.Signature{7, 8, 9}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		// Pass BridgeRequestDto instead of BridgeVSUDto
		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected BridgeVSUDto")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		txDto := BridgeVSUDto{
			SenderAddr:           "invalid-address",
			AddingValidatorAddrs: []string{solana.NewWallet().PublicKey().String()},
			BatchID:              1,
		}

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid sender address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid adding validator address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		txDto := BridgeVSUDto{
			SenderAddr:           senderWallet.PublicKey.String(),
			AddingValidatorAddrs: []string{"invalid-address"},
			BatchID:              1,
		}

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid adding validator address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid removing validator address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		txDto := BridgeVSUDto{
			SenderAddr:             senderWallet.PublicKey.String(),
			RemovingValidatorAddrs: []string{"invalid-address"},
			BatchID:                1,
		}

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid removing validator address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("empty both adding and removing", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		txDto := BridgeVSUDto{
			SenderAddr: senderWallet.PublicKey.String(),
			BatchID:    1,
		}

		expectedTx := &solana.Transaction{}
		expectedSig := solana.Signature{10, 11, 12}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeBridgeVsu,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})
}

func TestInitialize(t *testing.T) {
	ctx := context.Background()
	mockProvider := new(MockSenderTxProvider)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	programKeyPair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	recentBlockHash := solana.Hash{}

	instructionConfig := InstructionConfig{
		vaultPDA:                           solana.NewWallet().PublicKey(),
		validatorSetPDA:                    solana.NewWallet().PublicKey(),
		tokenRegistryPDA:                   solana.NewWallet().PublicKey(),
		tokenProgramID:                     solana.TokenProgramID,
		systemProgramID:                    solana.SystemProgramID,
		splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
		programKeyPair:                     programKeyPair,
	}

	senderWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	expectedSig := solana.Signature{}
	expectedTx := &solana.Transaction{}

	t.Run("success with 4 validators", func(t *testing.T) {
		validator1 := solana.NewWallet().PublicKey().String()
		validator2 := solana.NewWallet().PublicKey().String()
		validator3 := solana.NewWallet().PublicKey().String()
		validator4 := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			SenderAddr: senderWallet.PublicKey.String(),
			Validators: []string{validator1, validator2, validator3, validator4},
			LastID:     0,
		}

		expectedSig = solana.Signature{1, 2, 3}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("success with 10 validators", func(t *testing.T) {
		validators := make([]string, 10)
		for i := 0; i < 10; i++ {
			validators[i] = solana.NewWallet().PublicKey().String()
		}

		txDto := InitializeDto{
			SenderAddr: senderWallet.PublicKey.String(),
			Validators: validators,
			LastID:     42,
		}

		expectedSig = solana.Signature{4, 5, 6}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("success with empty validators list", func(t *testing.T) {
		txDto := InitializeDto{
			SenderAddr: senderWallet.PublicKey.String(),
			Validators: []string{},
			LastID:     0,
		}

		expectedSig = solana.Signature{7, 8, 9}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("invalid DTO type", func(t *testing.T) {
		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		// Pass BridgeRequestDto instead of InitializeDto
		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
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
			*senderWallet,
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "expected InitializeDto")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid sender address", func(t *testing.T) {
		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		txDto := InitializeDto{
			SenderAddr: "invalid-address",
			Validators: []string{solana.NewWallet().PublicKey().String()},
			LastID:     0,
		}

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid sender address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("invalid validator address", func(t *testing.T) {
		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		validValidator := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			SenderAddr: senderWallet.PublicKey.String(),
			Validators: []string{validValidator, "invalid-address"},
			LastID:     0,
		}

		_, err = txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid validator address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("duplicate validators", func(t *testing.T) {
		validator := solana.NewWallet().PublicKey().String()

		txDto := InitializeDto{
			SenderAddr: senderWallet.PublicKey.String(),
			Validators: []string{validator, validator},
			LastID:     0,
		}

		expectedSig = solana.Signature{13, 14, 15}

		mockProvider.On("CreateIxTransaction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
			recentBlockHash,
		).Return(expectedTx, nil)

		mockProvider.On("ExecuteTransaction",
			mock.Anything,
			expectedTx,
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		tx, err := txSender.CreateTx(
			ctx,
			*senderWallet,
			InstructionTypeInitialize,
			recentBlockHash,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, expectedTx, tx)

		sig, err := txSender.SendTx(
			context.Background(),
			*senderWallet,
			tx,
		)
		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})
}

type MockSenderTxProvider struct {
	mock.Mock
}

func (m *MockSenderTxProvider) CreateIxTransaction(ctx context.Context, ix *solana.Instruction, feePayer solana.PrivateKey, recentBlockHash solana.Hash) (*solana.Transaction, error) {
	args := m.Called(ctx, ix, feePayer, recentBlockHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*solana.Transaction), args.Error(1)
}

func (m *MockSenderTxProvider) ExecuteTransaction(ctx context.Context, tx *solana.Transaction, feePayer solana.PrivateKey) (*solana.Signature, error) {
	args := m.Called(ctx, tx, feePayer)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*solana.Signature), args.Error(1)
}
