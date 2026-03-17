package sendtx

import (
	"context"
	"fmt"
	"math/big"

	infracommon "github.com/Ethernal-Tech/cardano-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
)

type TxSender struct {
	txProvider        wallet.ITxProvider
	chainConfig       *ChainConfig
	instructionConfig *InstructionConfig
	retryOptions      []infracommon.RetryConfigOption
}

func NewTxSender(txProvider wallet.ITxProvider, chainConfig *ChainConfig) *TxSender {
	txSnd := &TxSender{
		txProvider:  txProvider,
		chainConfig: chainConfig,
	}

	instrCfg, _ := NewInstructionConfig() // without adding options, error will never be returned

	txSnd.instructionConfig = instrCfg

	return txSnd
}

// CreateTx builds a Solana transaction without signing it.
//
// The transaction is returned unsigned so batchers can deterministically
// construct the same transaction payload before producing signatures.
//
// If a signature is required, it can be added explicitly by calling tx.Sign(...)
// after the transaction has been created.
func (txSnd *TxSender) CreateTx(
	ctx context.Context,
	solanaPublicKey solana.PublicKey,
	instructionType InstructionType,
	recentBlockHash solana.Hash,
	txDto interface{},
) (*solana.Transaction, error) {
	instruction, err := txSnd.buildInstruction(instructionType, txDto)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare bridging request instruction: %w", err)
	}

	tx, err := solana.NewTransactionBuilder().SetRecentBlockHash(recentBlockHash).
		SetFeePayer(solanaPublicKey).AddInstruction(instruction).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build transaction: %w", err)
	}

	return tx, nil
}

func (txSnd *TxSender) SendTx(
	ctx context.Context,
	signedTransaction *solana.Transaction,
) (*solana.Signature, error) {
	signature, err := txSnd.txProvider.SendTransaction(ctx, signedTransaction)
	if err != nil {
		return nil, fmt.Errorf("failed to send %s transaction: %w", signedTransaction.Signatures[0], err)
	}

	return &signature, nil
}

func checkFees(config *ChainConfig, bridgingFee, operationFee uint64) error {
	if bridgingFee < config.MinFeeForBridging {
		return fmt.Errorf("bridging fee is less than: %d", config.MinFeeForBridging)
	}

	if operationFee < config.MinOperationFeeAmount {
		return fmt.Errorf("operation fee is less than: %d", config.MinOperationFeeAmount)
	}

	return nil
}

func (txSnd *TxSender) buildInstruction(
	instructionType InstructionType, txDto interface{}) (solana.Instruction, error) {
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
	case InstructionTypeBridgeVsu:
		tx, ok := txDto.(BridgeVSUDto)
		if !ok {
			return nil, fmt.Errorf("expected BridgeVSUDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildBridgeVSUInstruction(tx)
	case InstructionTypeInitialize:
		tx, ok := txDto.(InitializeDto)
		if !ok {
			return nil, fmt.Errorf("expected InitializeDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildInitializeInstruction(tx)
	case InstructionTypeRegisterTokensLockUnlock:
		tx, ok := txDto.(RegisterTokenLockUnlockDto)
		if !ok {
			return nil, fmt.Errorf("expected RegisterTokenLockUnlockDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildRegisterTokenLockUnlockInstruction(tx)
	case InstructionTypeUpdateFeeConfig:
		tx, ok := txDto.(UpdateFeeConfigDto)
		if !ok {
			return nil, fmt.Errorf("expected UpdateFeeConfigDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildUpdateFeeConfigInstruction(tx)
	case InstructionTypeSOLTransfer:
		tx, ok := txDto.(SOLTransferDto)
		if !ok {
			return nil, fmt.Errorf("expected TransferDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildTransferInstruction(tx)
	case InstructionTypeSPLTransfer:
		tx, ok := txDto.(SPLTransferDto)
		if !ok {
			return nil, fmt.Errorf("expected SPLTransferDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildSPLTransferInstruction(tx)
	case InstructionCreateInstruction:
		tx, ok := txDto.(CreateInstructionDto)
		if !ok {
			return nil, fmt.Errorf("expected CreateInstructionDto for type %s, got %T", instructionType, txDto)
		}

		return txSnd.buildCreateInstruction(tx)
	default:
		return nil, fmt.Errorf("unsupported transaction type: %s", instructionType)
	}
}

func (txSnd *TxSender) buildBridgingRequestInstruction(tx BridgeRequestDto) (solana.Instruction, error) {
	if err := checkFees(txSnd.chainConfig, tx.BridgingFee, tx.OperationFee); err != nil {
		return nil, fmt.Errorf("fees do not meet the minimum requirements: %w", err)
	}

	for _, receiver := range tx.Receivers {
		if receiver.TokenAmount.Amount.Cmp(new(big.Int).SetUint64(txSnd.chainConfig.MinAmountToBridge)) == -1 {
			return nil, fmt.Errorf("amount to bridge is less than the minimum required: %d", txSnd.chainConfig.MinAmountToBridge)
		}
	}

	if err := wallet.ValidateAddress(tx.SenderAddr, false); err != nil {
		return nil, fmt.Errorf("invalid sender address: %s: %w", tx.SenderAddr, err)
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender pub key from address %s: %w", tx.SenderAddr, err)
	}

	if err := wallet.ValidatePublicKey(txSnd.chainConfig.TreasuryAddress, false); err != nil {
		return nil, fmt.Errorf(
			"invalid treasury address %s in chain config: %w", txSnd.chainConfig.TreasuryAddress.String(), err)
	}

	if err := wallet.ValidatePublicKey(txSnd.chainConfig.BridgingFeeAddress, false); err != nil {
		return nil, fmt.Errorf(
			"invalid bridging fee address %s in chain config: %w", txSnd.chainConfig.BridgingFeeAddress.String(), err)
	}

	// for now, we only support txs with one receiver
	receiver := tx.Receivers[0]

	tokenMintPublicKey, err := wallet.PublicKeyFromAddress(receiver.TokenAmount.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token mint address: %s: %w", receiver.TokenAmount.TokenMint, err)
	}

	err = wallet.ValidatePublicKey(tokenMintPublicKey, true)
	if err != nil {
		return nil, fmt.Errorf("receiver has an invalid token mint specified: %s: %w", tokenMintPublicKey.String(), err)
	}

	if err = txSnd.instructionConfig.ApplyOptions(
		WithValidatorSetPDA(),
		WithVaultPDA(),
		WithTokenRegistryPDA(tokenMintPublicKey),
		WithFeeConfigPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	senderAta, _, err := wallet.FindAssociatedTokenAddress(senderPubKey, tokenMintPublicKey)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to find sender associated token account for token %s: %w", tokenMintPublicKey.String(), err)
	}

	vaultAta, _, err := wallet.FindAssociatedTokenAddress(txSnd.instructionConfig.vaultPDA, tokenMintPublicKey)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to find vault associated token account for token %s: %w", tokenMintPublicKey.String(), err)
	}

	return skyline_program.NewBridgeRequestInstruction(
		receiver.TokenAmount.Amount.Uint64(),
		receiver.Address,
		tx.DstChainID,
		tx.BridgingFee+tx.OperationFee,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		senderAta,
		txSnd.instructionConfig.vaultPDA,
		vaultAta,
		tokenMintPublicKey,
		txSnd.instructionConfig.tokenRegistryPDA,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.splAssociatedTokenAccountProgramID,
		txSnd.instructionConfig.feeConfigPDA,
		txSnd.chainConfig.TreasuryAddress,
		txSnd.chainConfig.BridgingFeeAddress,
	)
}

func (txSnd *TxSender) buildBridgeTransactionInstruction(tx BridgeTransactionDto) (solana.Instruction, error) {
	if err := wallet.ValidateAddress(tx.SenderAddr, false); err != nil {
		return nil, fmt.Errorf("invalid sender address: %s: %w", tx.SenderAddr, err)
	}

	mintIndex := make(map[solana.PublicKey]uint8)

	var mints []solana.PublicKey

	transferItems := make([]skyline_program.TransferItem, 0, len(tx.Receivers))

	for _, receiver := range tx.Receivers {
		if err := wallet.ValidateAddress(receiver.Address, false); err != nil {
			return nil, fmt.Errorf("invalid receiver address: %s: %w", receiver.Address, err)
		}

		receiverPubKey, err := wallet.PublicKeyFromAddress(receiver.Address)
		if err != nil {
			return nil, fmt.Errorf("failed to parse receiver public key from address %s: %w", receiverPubKey.String(), err)
		}

		mint, err := wallet.PublicKeyFromAddress(receiver.TokenAmount.TokenMint)
		if err != nil {
			return nil, fmt.Errorf("failed to parse token mint address: %s: %w", receiver.TokenAmount.TokenMint, err)
		}

		idx, exists := mintIndex[mint]
		if !exists {
			idx = uint8(len(mints)) //nolint:gosec // number of mints is bounded by protocol
			mintIndex[mint] = idx

			mints = append(mints, mint)
		}

		transferItems = append(transferItems, skyline_program.TransferItem{
			Recipient: receiverPubKey,
			MintIndex: idx,
			Amount:    receiver.TokenAmount.Amount.Uint64(),
		})
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.SenderAddr, err)
	}

	if err = txSnd.instructionConfig.ApplyOptions(WithValidatorSetPDA(), WithVaultPDA()); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	return skyline_program.NewBridgeTransactionInstruction(
		transferItems,
		mints,
		tx.BatchID,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		txSnd.instructionConfig.vaultPDA,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.splAssociatedTokenAccountProgramID,
	)
}

func (txSnd *TxSender) buildBridgeVSUInstruction(tx BridgeVSUDto) (solana.Instruction, error) {
	if err := wallet.ValidateAddress(tx.SenderAddr, true); err != nil {
		return nil, fmt.Errorf("invalid sender address: %s: %w", tx.SenderAddr, err)
	}

	addingValidatorPubKeys := make([]solana.PublicKey, len(tx.AddingValidatorAddrs))
	removingValidatorPubKeys := make([]solana.PublicKey, len(tx.RemovingValidatorAddrs))

	for i, addingValidator := range tx.AddingValidatorAddrs {
		if err := wallet.ValidateAddress(addingValidator, true); err != nil {
			return nil, fmt.Errorf("invalid adding validator address %s: %w", addingValidator, err)
		}

		addingKey, err := wallet.PublicKeyFromAddress(addingValidator)
		if err != nil {
			return nil, fmt.Errorf("failed to parse adding validator public key from address %s: %w", addingValidator, err)
		}

		addingValidatorPubKeys[i] = addingKey
	}

	for i, removingValidator := range tx.RemovingValidatorAddrs {
		if err := wallet.ValidateAddress(removingValidator, true); err != nil {
			return nil, fmt.Errorf("invalid removing validator address %s: %w", removingValidator, err)
		}

		removingKey, err := wallet.PublicKeyFromAddress(removingValidator)
		if err != nil {
			return nil, fmt.Errorf("failed to parse removing validator public key from address %s: %w", removingValidator, err)
		}

		removingValidatorPubKeys[i] = removingKey
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.SenderAddr, err)
	}

	if err = txSnd.instructionConfig.ApplyOptions(WithValidatorSetPDA()); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	return skyline_program.NewBridgeVsuInstruction(
		addingValidatorPubKeys,
		removingValidatorPubKeys,
		tx.BatchID,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		txSnd.instructionConfig.systemProgramID,
	)
}

func (txSnd *TxSender) buildInitializeInstruction(tx InitializeDto) (solana.Instruction, error) {
	if err := wallet.ValidateAddress(tx.AuthorityAddr, true); err != nil {
		return nil, fmt.Errorf("invalid sender address: %s: %w", tx.AuthorityAddr, err)
	}

	validatorPubKeys := make([]solana.PublicKey, len(tx.Validators))

	for i, validatorAddr := range tx.Validators {
		if err := wallet.ValidateAddress(validatorAddr, true); err != nil {
			return nil, fmt.Errorf("invalid validator address: %s: %w", validatorAddr, err)
		}

		validatorKey, err := wallet.PublicKeyFromAddress(validatorAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key from validator address %s: %w", validatorAddr, err)
		}

		validatorPubKeys[i] = validatorKey
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.AuthorityAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.AuthorityAddr, err)
	}

	if err = txSnd.instructionConfig.ApplyOptions(
		WithValidatorSetPDA(), WithVaultPDA(), WithFeeConfigPDA()); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	return skyline_program.NewInitializeInstruction(
		validatorPubKeys,
		&tx.LastID,
		txSnd.chainConfig.MinOperationFeeAmount,
		txSnd.chainConfig.MinFeeForBridging,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		txSnd.instructionConfig.vaultPDA,
		txSnd.instructionConfig.feeConfigPDA,
		txSnd.chainConfig.TreasuryAddress,
		txSnd.chainConfig.BridgingFeeAddress,
		txSnd.instructionConfig.systemProgramID,
	)
}

func (txSnd *TxSender) buildRegisterTokenLockUnlockInstruction(
	tx RegisterTokenLockUnlockDto) (solana.Instruction, error) {
	if err := wallet.ValidateAddress(tx.AuthorityAddr, true); err != nil {
		return nil, fmt.Errorf("invalid authority address: %s: %w", tx.AuthorityAddr, err)
	}

	authorityPubKey, err := wallet.PublicKeyFromAddress(tx.AuthorityAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse authority public key from address %s: %w", tx.AuthorityAddr, err)
	}

	tokenMintPublicKey, err := wallet.PublicKeyFromAddress(tx.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token mint address: %s: %w", tx.TokenMint, err)
	}

	err = wallet.ValidatePublicKey(tokenMintPublicKey, true)
	if err != nil {
		return nil, fmt.Errorf("invalid token mint specified: %s: %w", tokenMintPublicKey.String(), err)
	}

	if err = txSnd.instructionConfig.ApplyOptions(
		WithFeeConfigPDA(),
		WithTokenRegistryPDA(tokenMintPublicKey),
		WithTokenIDGuardPDA(tx.TokenID),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	return skyline_program.NewRegisterLockUnlockTokenInstruction(
		tx.TokenID,
		tx.MinBridgingAmount,
		authorityPubKey,
		txSnd.instructionConfig.feeConfigPDA,
		tokenMintPublicKey,
		txSnd.instructionConfig.tokenRegistryPDA,
		txSnd.instructionConfig.tokenIDGuardPDA,
		txSnd.instructionConfig.systemProgramID,
	)
}

func (txSnd *TxSender) buildUpdateFeeConfigInstruction(tx UpdateFeeConfigDto) (solana.Instruction, error) {
	if err := wallet.ValidateAddress(tx.AuthorityAddr, true); err != nil {
		return nil, fmt.Errorf("invalid authority address: %s: %w", tx.AuthorityAddr, err)
	}

	authorityPubKey, err := wallet.PublicKeyFromAddress(tx.AuthorityAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse authority public key from address %s: %w", tx.AuthorityAddr, err)
	}

	newTreasuryAcc, newRelayerAcc := solana.PublicKey{}, solana.PublicKey{}

	if tx.UpdateTreasury && tx.NewTreasuryAddress != "" {
		if err := wallet.ValidateAddress(tx.NewTreasuryAddress, true); err != nil {
			return nil, fmt.Errorf("invalid new treasury address: %s: %w", tx.NewTreasuryAddress, err)
		}

		newTreasuryAcc, err = wallet.PublicKeyFromAddress(tx.NewTreasuryAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to parse new treasury public key from address %s: %w", tx.NewTreasuryAddress, err)
		}
	}

	if tx.UpdateRelayer && tx.NewRelayerAddress != "" {
		if err := wallet.ValidateAddress(tx.NewRelayerAddress, true); err != nil {
			return nil, fmt.Errorf("invalid new relayer address: %s: %w", tx.NewRelayerAddress, err)
		}

		newRelayerAcc, err = wallet.PublicKeyFromAddress(tx.NewRelayerAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to parse new relayer public key from address %s: %w", tx.NewRelayerAddress, err)
		}
	}

	return skyline_program.NewUpdateFeeConfigInstruction(
		&tx.MinOperationFee,
		&tx.BridgingFee,
		&tx.UpdateTreasury,
		&tx.UpdateRelayer,
		authorityPubKey,
		txSnd.instructionConfig.feeConfigPDA,
		newTreasuryAcc,
		newRelayerAcc,
	)
}

func (txSnd *TxSender) buildTransferInstruction(tx SOLTransferDto) (solana.Instruction, error) {
	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderPublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.SenderPublicKey, err)
	}

	receiverPubKey, err := wallet.PublicKeyFromAddress(tx.ReceiverPublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse receiver public key from address %s: %w", tx.ReceiverPublicKey, err)
	}

	return system.NewTransferInstruction(
		tx.Amount,
		senderPubKey,
		receiverPubKey,
	).Build(), nil
}

func (txSnd *TxSender) buildSPLTransferInstruction(tx SPLTransferDto) (solana.Instruction, error) {
	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderPublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.SenderPublicKey, err)
	}

	receiverPubKey, err := wallet.PublicKeyFromAddress(tx.ReceiverPublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse receiver public key from address %s: %w", tx.ReceiverPublicKey, err)
	}

	mintTokenAddress, err := wallet.PublicKeyFromAddress(tx.MintTokenAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token mint from address %s: %w", tx.MintTokenAddress, err)
	}

	sourceAta, _, err := wallet.FindAssociatedTokenAddress(senderPubKey, mintTokenAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to find source associated token address: %w", err)
	}

	destinationAta, _, err := wallet.FindAssociatedTokenAddress(receiverPubKey, mintTokenAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to find destination associated token address: %w", err)
	}

	return token.NewTransferInstruction(
		tx.Amount,
		sourceAta,
		destinationAta,
		senderPubKey,
		[]solana.PublicKey{},
	).Build(), nil
}

func (txSnd *TxSender) buildCreateInstruction(tx CreateInstructionDto) (solana.Instruction, error) {
	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderPublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.SenderPublicKey, err)
	}

	receiverPubKey, err := wallet.PublicKeyFromAddress(tx.ReceiverPublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse receiver public key from address %s: %w", tx.ReceiverPublicKey, err)
	}

	mintTokenAddress, err := wallet.PublicKeyFromAddress(tx.MintTokenAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token mint from address %s: %w", tx.MintTokenAddress, err)
	}

	return associatedtokenaccount.NewCreateInstruction(
		senderPubKey,
		receiverPubKey,
		mintTokenAddress,
	).Build(), nil
}
