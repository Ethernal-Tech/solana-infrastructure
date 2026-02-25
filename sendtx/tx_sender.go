package sendtx

import (
	"context"
	"encoding/binary"
	"fmt"

	infracommon "github.com/Ethernal-Tech/cardano-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
)

type SenderTxProvider interface {
	ExecuteInstruction(ctx context.Context, ix *solana.Instruction, feePayer solana.PrivateKey) (*solana.Signature, error)
}

var _ SenderTxProvider = (*wallet.Provider)(nil)

type TxSender struct {
	txProvider        SenderTxProvider
	minAmountToBridge uint64
	chainConfig       ChainConfig
	instructionConfig InstructionConfig
	retryOptions      []infracommon.RetryConfigOption
}

func NewTxSender(txProvider SenderTxProvider,
	chainConfig ChainConfig, instructionConfig InstructionConfig,
) (*TxSender, error) {
	if err := instructionConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid instruction config: %w", err)
	}

	txSnd := &TxSender{
		txProvider:        txProvider,
		chainConfig:       chainConfig,
		instructionConfig: instructionConfig,
	}

	txSnd.minAmountToBridge = max(txSnd.minAmountToBridge, chainConfig.MinAmountToBridge)

	return txSnd, nil
}

func (txSnd *TxSender) CreateTx(
	ctx context.Context,
	solanaWallet wallet.Wallet,
	instructionType InstructionType,
	txDto interface{},
) (*solana.Signature, error) {
	instruction, err := txSnd.buildInstruction(instructionType, txDto)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare bridging request instruction: %w", err)
	}

	signature, err := txSnd.sendTransaction(ctx, instruction, solanaWallet)
	if err != nil {
		return nil, fmt.Errorf("failed to send bridging request transaction: %w", err)
	}

	return signature, nil
}

func (txSnd *TxSender) sendTransaction(
	ctx context.Context, instruction solana.Instruction, solanaWallet wallet.Wallet) (*solana.Signature, error) {
	return txSnd.txProvider.ExecuteInstruction(ctx, &instruction, solanaWallet.PrivateKey)
}

func checkFees(config ChainConfig, bridgingFee, operationFee uint64) error {
	if bridgingFee < config.MinFeeForBridging {
		return fmt.Errorf("bridging fee is less than: %d", config.MinFeeForBridging)
	}

	if operationFee < config.MinOperationFeeAmount {
		return fmt.Errorf("operation fee is less than: %d", config.MinOperationFeeAmount)
	}

	return nil
}

func (txSnd *TxSender) buildInstruction(instructionType InstructionType, txDto interface{}) (solana.Instruction, error) {
	switch instructionType {
	case InstructionTypeBridgingRequest:
		tx, ok := txDto.(BridgeRequestDto)
		if !ok {
			return nil, fmt.Errorf("expected BridgeRequestDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildBridgingRequestInstruction(tx)
	case InstructionTypeBridgeTransaction:
		tx, ok := txDto.(BridgeTransactionDto)
		if !ok {
			return nil, fmt.Errorf("expected BridgeTransactionDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildBridgeTransactionInstruction(tx)
	default:
		return nil, fmt.Errorf("unsupported transaction type: %s", instructionType)
	}
}

func (txSnd *TxSender) buildBridgingRequestInstruction(tx BridgeRequestDto) (solana.Instruction, error) {
	if err := checkFees(txSnd.chainConfig, tx.BridgingFee, tx.OperationFee); err != nil {
		return nil, fmt.Errorf("fees do not meet the minimum requirements: %w", err)
	}

	for _, receiver := range tx.Receivers {
		if receiver.TokenAmount.Amount < txSnd.minAmountToBridge {
			return nil, fmt.Errorf("amount to bridge is less than the minimum required: %d", txSnd.minAmountToBridge)
		}
	}

	if err := wallet.ValidateAddress(tx.SenderAddr, false); err != nil {
		return nil, fmt.Errorf("invalid sender address: %w", err)
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender address: %w", err)
	}

	receiver := tx.Receivers[0]

	err = wallet.ValidatePublicKey(receiver.TokenAmount.TokenMint, true)
	if err != nil {
		return nil, fmt.Errorf("receiver has an invalid token mint specified: %w", err)
	}

	senderAta, _, err := wallet.FindAssociatedTokenAddress(senderPubKey, receiver.TokenAmount.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("failed to find sender associated token account: %w", err)
	}

	vaultAta, _, err := wallet.FindAssociatedTokenAddress(txSnd.instructionConfig.vaultPDA, receiver.TokenAmount.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("failed to find vault associated token account: %w", err)
	}

	return skyline_program.NewBridgeRequestInstruction(
		receiver.TokenAmount.Amount,
		[]byte(receiver.Addr),
		tx.DstChainID,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		senderAta,
		txSnd.instructionConfig.vaultPDA,
		vaultAta,
		receiver.TokenAmount.TokenMint,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.SPLAssociatedTokenAccountProgramID,
	)
}

func (txSnd *TxSender) buildBridgeTransactionInstruction(tx BridgeTransactionDto) (solana.Instruction, error) {
	if err := wallet.ValidateAddress(tx.SenderAddr, false); err != nil {
		return nil, fmt.Errorf("invalid sender address: %w", err)
	}

	receiver := tx.Receivers[0]

	if err := wallet.ValidateAddress(receiver.Addr, false); err != nil {
		return nil, fmt.Errorf("invalid receiver address: %w", err)
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key: %w", err)
	}

	receiverPubKey, err := wallet.PublicKeyFromAddress(receiver.Addr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse receiver public key: %w", err)
	}

	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, 1)

	bridgingTransactionPda, _, err := solana.FindProgramAddress(
		[][]byte{skyline_program.BRIDGING_TRANSACTION_SEED, buf}, txSnd.instructionConfig.programKeyPair.PublicKey())
	if err != nil {
		return nil, fmt.Errorf("failed to find bridging transaction PDA: %w", err)
	}

	receiverAta, _, err := wallet.FindAssociatedTokenAddress(receiverPubKey, receiver.TokenAmount.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("failed to find receiver associated token account: %w", err)
	}

	vaultAta, _, err := wallet.FindAssociatedTokenAddress(txSnd.instructionConfig.vaultPDA, receiver.TokenAmount.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("failed to find vault associated token account: %w", err)
	}

	return skyline_program.NewBridgeTransactionInstruction(
		receiver.TokenAmount.Amount,
		tx.BatchID,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		bridgingTransactionPda,
		receiver.TokenAmount.TokenMint,
		receiverPubKey,
		receiverAta,
		txSnd.instructionConfig.vaultPDA,
		vaultAta,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.SPLAssociatedTokenAccountProgramID,
	)
}
