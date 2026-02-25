package sendtx

import (
	"context"
	"errors"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/test-go/testify/assert"
)

func TestTxSender(t *testing.T) {
	mockProvider := new(MockSenderTxProvider)

	programKeyPair, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	t.Run("NewTxSender - valid config", func(t *testing.T) {

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			instructionConfig,
		)

		require.NoError(t, err)
		require.NotNil(t, txSender)
	})

	t.Run("NewTxSender - missing vault PDA", func(t *testing.T) {
		mockProvider := new(MockSenderTxProvider)

		instructionConfig := InstructionConfig{
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			instructionConfig,
		)

		require.Error(t, err, "vaultPDA")
		require.Nil(t, txSender)
	})

	t.Run("NewTxSender - missing validator set PDA", func(t *testing.T) {
		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
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
			ChainConfig{},
			InstructionConfig{
				vaultPDA:                           solana.NewWallet().PublicKey(),
				validatorSetPDA:                    solana.NewWallet().PublicKey(),
				tokenProgramID:                     solana.TokenProgramID,
				systemProgramID:                    solana.SystemProgramID,
				SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
				programKeyPair:                     programKeyPair,
			},
		)
		require.NoError(t, err)

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			"wrongIxType",
			interface{}(nil),
		)

		require.Error(t, err, "unsupported transaction type")
	})

	t.Run("BridgingRequest - success", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey()

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: tokenMint,
					},
				},
			},
		}

		expectedSig := solana.Signature{1, 2, 3}

		mockProvider.On("ExecuteInstruction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{MinAmountToBridge: 0},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("BridgingRequest - insuficient bridging amount", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey()

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    1_000_000,
						TokenMint: tokenMint,
					},
				},
			},
			BridgingFee: 1_000_000_000,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				MinAmountToBridge: 1_000_000_000,
				MinFeeForBridging: 1_000_000_000,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		require.Error(t, err, "amount to bridge is less than the minimum required")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("BridgingRequest - insuficient bridging fee", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey()

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    1_000_000_000,
						TokenMint: tokenMint,
					},
				},
			},
			BridgingFee: 1_000_000,
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{
				MinAmountToBridge: 1_000_000_000,
				MinFeeForBridging: 1_000_000_000,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		require.Error(t, err, "fees do not meet the minimum requirements")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("BridgingRequest - insuficient operation fee", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		tokenMint := solana.NewWallet().PublicKey()

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    1_000_000_000,
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
				MinAmountToBridge:     1_000_000_000,
				MinFeeForBridging:     1_000_000_000,
				MinOperationFeeAmount: 1_000_000_000,
			},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		require.Error(t, err, "fees do not meet the minimum requirements")
		require.Nil(t, sig)
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("BridgingRequest - invalid DTO type", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			InstructionConfig{
				vaultPDA:                           solana.NewWallet().PublicKey(),
				validatorSetPDA:                    solana.NewWallet().PublicKey(),
				tokenProgramID:                     solana.TokenProgramID,
				systemProgramID:                    solana.SystemProgramID,
				SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
				programKeyPair:                     programKeyPair,
			},
		)
		require.NoError(t, err)

		// Pass BridgeTransactionDto instead of BridgeRequestDto
		txDto := BridgeTransactionDto{
			SenderAddr: senderWallet.PublicKey.String(),
			BatchID:    42,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "invalid-address",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: solana.NewWallet().PublicKey(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		require.Error(t, err, "Expected BridgeRequestDto")
	})

	t.Run("BridgingRequest - invalid sender address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			InstructionConfig{
				vaultPDA:                           solana.NewWallet().PublicKey(),
				validatorSetPDA:                    solana.NewWallet().PublicKey(),
				tokenProgramID:                     solana.TokenProgramID,
				systemProgramID:                    solana.SystemProgramID,
				SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
				programKeyPair:                     programKeyPair,
			},
		)
		require.NoError(t, err)

		txDto := BridgeRequestDto{
			SenderAddr: "invalid-address",
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: solana.NewWallet().PublicKey(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		require.Error(t, err, "invalid sender address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("BridgingRequest - provider error", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)
		tokenMint := solana.NewWallet().PublicKey()

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: tokenMint,
					},
				},
			},
		}

		expectedErr := errors.New("mocked error")
		mockProvider.On("ExecuteInstruction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
		).Return(nil, expectedErr)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			instructionConfig,
		)
		require.NoError(t, err)

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		require.Error(t, err, "failed to send bridging request transaction")
		mockProvider.AssertExpectations(t)
	})

	t.Run("BridgingRequest - invalid token mint in receiver", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txDto := BridgeRequestDto{
			SenderAddr: senderWallet.PublicKey.String(),
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: solana.PublicKey{}, // Zero address - invalid
					},
				},
			},
		}

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{MinAmountToBridge: 0},
			instructionConfig,
		)
		require.NoError(t, err)

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgingRequest,
			txDto,
		)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid token mint specified")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("BridgeTransaction - success", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		receiverPubKey := solana.NewWallet().PublicKey()
		tokenMint := solana.NewWallet().PublicKey()

		instructionConfig := InstructionConfig{
			vaultPDA:                           solana.NewWallet().PublicKey(),
			validatorSetPDA:                    solana.NewWallet().PublicKey(),
			tokenProgramID:                     solana.TokenProgramID,
			systemProgramID:                    solana.SystemProgramID,
			SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
			programKeyPair:                     programKeyPair,
		}

		txDto := BridgeTransactionDto{
			SenderAddr: senderWallet.PublicKey.String(),
			BatchID:    42,
			Receivers: []BridgingTxReceiver{
				{
					Addr: receiverPubKey.String(),
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: tokenMint,
					},
				},
			},
		}

		expectedSig := solana.Signature{4, 5, 6}

		mockProvider.On("ExecuteInstruction",
			mock.Anything,
			mock.AnythingOfType("*solana.Instruction"),
			senderWallet.PrivateKey,
		).Return(&expectedSig, nil)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			instructionConfig,
		)
		require.NoError(t, err)

		sig, err := txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgeTransaction,
			txDto,
		)

		require.NoError(t, err)
		require.Equal(t, &expectedSig, sig)
		mockProvider.AssertExpectations(t)
	})

	t.Run("BridgeTransaction - invalid DTO type", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			InstructionConfig{
				vaultPDA:                           solana.NewWallet().PublicKey(),
				validatorSetPDA:                    solana.NewWallet().PublicKey(),
				tokenProgramID:                     solana.TokenProgramID,
				systemProgramID:                    solana.SystemProgramID,
				SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
				programKeyPair:                     programKeyPair,
			},
		)
		require.NoError(t, err)

		// Pass BridgeRequestDto instead of BridgeTransactionDto
		txDto := BridgeRequestDto{
			SenderAddr: solana.NewWallet().PublicKey().String(),
			DstChainID: 1,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: solana.NewWallet().PublicKey(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgeTransaction,
			txDto,
		)

		require.Error(t, err, "expected BridgeTransactionDto")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("BridgeTransaction - invalid sender address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			InstructionConfig{
				vaultPDA:                           solana.NewWallet().PublicKey(),
				validatorSetPDA:                    solana.NewWallet().PublicKey(),
				tokenProgramID:                     solana.TokenProgramID,
				systemProgramID:                    solana.SystemProgramID,
				SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
				programKeyPair:                     programKeyPair,
			},
		)
		require.NoError(t, err)

		txDto := BridgeTransactionDto{
			SenderAddr: "invalid-address",
			BatchID:    42,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "0x1234567890123456789012345678901234567890",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: solana.NewWallet().PublicKey(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgeTransaction,
			txDto,
		)

		require.Error(t, err, "invalid receiver address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})

	t.Run("BridgeTransaction - invalid receiver address", func(t *testing.T) {
		senderWallet, err := wallet.NewWallet()
		require.NoError(t, err)

		txSender, err := NewTxSender(
			mockProvider,
			ChainConfig{},
			InstructionConfig{
				vaultPDA:                           solana.NewWallet().PublicKey(),
				validatorSetPDA:                    solana.NewWallet().PublicKey(),
				tokenProgramID:                     solana.TokenProgramID,
				systemProgramID:                    solana.SystemProgramID,
				SPLAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
				programKeyPair:                     programKeyPair,
			},
		)
		require.NoError(t, err)

		txDto := BridgeTransactionDto{
			SenderAddr: senderWallet.PublicKey.String(),
			BatchID:    42,
			Receivers: []BridgingTxReceiver{
				{
					Addr: "invalid-address",
					TokenAmount: wallet.TokenAmount{
						Amount:    1000,
						TokenMint: solana.NewWallet().PublicKey(),
					},
				},
			},
		}

		_, err = txSender.CreateTx(
			context.Background(),
			*senderWallet,
			InstructionTypeBridgeTransaction,
			txDto,
		)

		require.Error(t, err, "invalid receiver address")
		mockProvider.AssertNotCalled(t, "ExecuteInstruction")
	})
}

type MockSenderTxProvider struct {
	mock.Mock
}

func (m *MockSenderTxProvider) ExecuteInstruction(ctx context.Context, ix *solana.Instruction, feePayer solana.PrivateKey) (*solana.Signature, error) {
	args := m.Called(ctx, ix, feePayer)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*solana.Signature), args.Error(1)
}
