package sendtx

import (
	"context"
	"errors"
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
				BridgingFeeAddress:    feeWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				MinAmountToBridge:  0,
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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

	feeWallet, err := wallet.NewWallet()
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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

	feeWallet, err := wallet.NewWallet()
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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

	feeWallet, err := wallet.NewWallet()
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
		mockProvider := new(wallet.MockTxProvider)
		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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

	feeWallet, err := wallet.NewWallet()
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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

	feeWallet, err := wallet.NewWallet()
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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

	feeWallet, err := wallet.NewWallet()
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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

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
		mockProvider := new(wallet.MockTxProvider)

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
		mockProvider := new(wallet.MockTxProvider)

		txSender := NewTxSender(
			mockProvider,
			&ChainConfig{
				TreasuryAddress:    treasuryWallet.PublicKey,
				BridgingFeeAddress: feeWallet.PublicKey,
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
		mockProvider := new(wallet.MockTxProvider)

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
		mockProvider := new(wallet.MockTxProvider)

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

func TestTxSender_GetProgramConfig(t *testing.T) {
	ctx := context.Background()

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	chainConfig := &ChainConfig{
		TreasuryAddress:    treasuryWallet.PublicKey,
		BridgingFeeAddress: feeWallet.PublicKey,
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

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	chainConfig := &ChainConfig{
		TreasuryAddress:    treasuryWallet.PublicKey,
		BridgingFeeAddress: feeWallet.PublicKey,
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
		TreasuryAddress:    solana.MustPublicKeyFromBase58("AXXWYCH6PNm6AGjaasPG1maarfQvRedSw18wj91Nem1F"),
		BridgingFeeAddress: solana.MustPublicKeyFromBase58("7d5xBAeX92qPugMB5vixR1cy3wpRCxKE7ckShZaJbPPL"),
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
