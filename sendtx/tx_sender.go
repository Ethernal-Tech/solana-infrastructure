package sendtx

import (
	"context"
	"encoding/binary"
	"fmt"

	infracommon "github.com/Ethernal-Tech/cardano-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	computebudget "github.com/gagliardetto/solana-go/programs/compute-budget"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
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
//
// Additional behavior can be configured via CreateTxOption values. In
// particular, WithAddressLookupTables enables Address Lookup Tables for
// instructions that reference many accounts (e.g. bridge_transaction),
// producing a v0 transaction that fits within the 1232-byte size limit.
func (txSnd *TxSender) CreateTx(
	ctx context.Context,
	solanaPublicKey solana.PublicKey,
	instructionType InstructionType,
	recentBlockHash solana.Hash,
	txDto interface{},
	opts ...CreateTxOption,
) (*solana.Transaction, error) {
	cfg := applyCreateTxOptions(opts)

	instructions, err := txSnd.buildInstructions(instructionType, txDto)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare bridging request instruction: %w", err)
	}

	builder := solana.NewTransactionBuilder().SetRecentBlockHash(recentBlockHash).SetFeePayer(solanaPublicKey)

	for _, instruction := range instructions {
		builder = builder.AddInstruction(instruction)
	}

	if len(cfg.addressTables) > 0 {
		builder = builder.WithOpt(solana.TransactionAddressTables(cfg.addressTables))
	}

	tx, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build transaction: %w", err)
	}

	// The builder already emits a v0 message when address tables are provided,
	// but make the requirement explicit here in case that behavior ever
	// changes: legacy messages cannot carry AddressTableLookups.
	if len(cfg.addressTables) > 0 {
		tx.Message.SetVersion(solana.MessageVersionV0)
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

func (txSnd *TxSender) buildInstructions(
	instructionType InstructionType, txDto interface{}) ([]solana.Instruction, error) {
	switch instructionType {
	case InstructionTypeBridgingRequest:
		tx, ok := txDto.(BridgeRequestDto)
		if !ok {
			return nil, fmt.Errorf("expected BridgeRequestDto for type %s, got %T", instructionType, txDto)
		}

		bridgingIx, err := txSnd.buildBridgingRequestInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build bridging request instruction: %w", err)
		}

		return []solana.Instruction{bridgingIx}, nil
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

		bridgeVSUIx, err := txSnd.buildBridgeVSUInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build bridge VSU instruction: %w", err)
		}

		return []solana.Instruction{bridgeVSUIx}, nil
	case InstructionTypeHotWalletIncrement:
		tx, ok := txDto.(HotWalletIncrementDto)
		if !ok {
			return nil, fmt.Errorf("expected HotWalletIncrementDto for type %s, got %T", instructionType, txDto)
		}

		hotWalletIncrementIx, err := txSnd.buildHotWalletIncrementInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build hot wallet increment instruction: %w", err)
		}

		return []solana.Instruction{hotWalletIncrementIx}, nil
	case InstructionTypeInitialize:
		tx, ok := txDto.(InitializeDto)
		if !ok {
			return nil, fmt.Errorf("expected InitializeDto for type %s, got %T", instructionType, txDto)
		}

		initializeIx, err := txSnd.buildInitializeInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build initialize instruction: %w", err)
		}

		return []solana.Instruction{initializeIx}, nil
	case InstructionTypeRegisterTokensLockUnlock:
		tx, ok := txDto.(RegisterTokenLockUnlockDto)
		if !ok {
			return nil, fmt.Errorf("expected RegisterTokenLockUnlockDto for type %s, got %T", instructionType, txDto)
		}

		registerTokenLockUnlockIx, err := txSnd.buildRegisterTokenLockUnlockInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build register token lock unlock instruction: %w", err)
		}

		return []solana.Instruction{registerTokenLockUnlockIx}, nil
	case InstructionTypeRegisterTokensMintBurn:
		tx, ok := txDto.(RegisterTokenMintBurnDto)
		if !ok {
			return nil, fmt.Errorf("expected RegisterTokenMintBurnDto for type %s, got %T", instructionType, txDto)
		}

		registerTokenMintBurnIx, err := txSnd.buildRegisterTokenMintBurnInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build register token mint burn instruction: %w", err)
		}

		return []solana.Instruction{registerTokenMintBurnIx}, nil
	case InstructionTypeUpdateFeeConfig:
		tx, ok := txDto.(UpdateFeeConfigDto)
		if !ok {
			return nil, fmt.Errorf("expected UpdateFeeConfigDto for type %s, got %T", instructionType, txDto)
		}

		updateFeeConfigIx, err := txSnd.buildUpdateFeeConfigInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build update fee config instruction: %w", err)
		}

		return []solana.Instruction{updateFeeConfigIx}, nil
	case InstructionTypeUpdateProgramVersion:
		tx, ok := txDto.(UpdateProgramVersionDto)
		if !ok {
			return nil, fmt.Errorf("expected UpdateProgramVersionDto for type %s, got %T", instructionType, txDto)
		}

		updateProgramVersionIx, err := txSnd.buildUpdateProgramVersionInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build update program version instruction: %w", err)
		}

		return []solana.Instruction{updateProgramVersionIx}, nil
	case InstructionTypeSOLTransfer:
		tx, ok := txDto.(SOLTransferDto)
		if !ok {
			return nil, fmt.Errorf("expected TransferDto for type %s, got %T", instructionType, txDto)
		}

		transferIx, err := txSnd.buildTransferInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build transfer instruction: %w", err)
		}

		return []solana.Instruction{transferIx}, nil
	case InstructionTypeSPLTransfer:
		tx, ok := txDto.(SPLTransferDto)
		if !ok {
			return nil, fmt.Errorf("expected SPLTransferDto for type %s, got %T", instructionType, txDto)
		}

		splTransferIx, err := txSnd.buildSPLTransferInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build SPL transfer instruction: %w", err)
		}

		return []solana.Instruction{splTransferIx}, nil
	case InstructionCreateInstruction:
		tx, ok := txDto.(CreateInstructionDto)
		if !ok {
			return nil, fmt.Errorf("expected CreateInstructionDto for type %s, got %T", instructionType, txDto)
		}

		createIx, err := txSnd.buildCreateInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build create instruction: %w", err)
		}

		return []solana.Instruction{createIx}, nil
	case InstructionTypeUpdateMinBridgingAmount:
		tx, ok := txDto.(UpdateMinBridgingAmountDto)
		if !ok {
			return nil, fmt.Errorf("expected UpdateMinBridgingAmountDto for type %s, got %T", instructionType, txDto)
		}

		updateMinBridgingAmountIx, err := txSnd.buildUpdateMinBridgingAmountInstruction(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to build update min bridging amount instruction: %w", err)
		}

		return []solana.Instruction{updateMinBridgingAmountIx}, nil
	default:
		return nil, fmt.Errorf("unsupported transaction type: %s", instructionType)
	}
}

func (txSnd *TxSender) buildBridgingRequestInstruction(tx BridgeRequestDto) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

	if err := checkFees(txSnd.chainConfig, tx.BridgingFee, tx.OperationFee); err != nil {
		return nil, fmt.Errorf("fees do not meet the minimum requirements: %w", err)
	}

	for _, receiver := range tx.Receivers {
		if receiver.TokenAmount.Amount < txSnd.chainConfig.MinAmountToBridge {
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

	// for now, we only support txs with one receiver
	receiver := tx.Receivers[0]

	if err = txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithValidatorSetPDA(),
		WithVaultPDA(),
		WithTokenRegistryPDA(receiver.TokenAmount.TokenID),
		WithFeeConfigPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	registeredTokens, err := txSnd.GetAllRegisteredTokens(tx.Ctx, programID)
	if err != nil {
		return nil, fmt.Errorf("failed to get all registered tokens: %w", err)
	}

	tokenMintPublicKey := registeredTokens[receiver.TokenAmount.TokenID].Mint

	err = wallet.ValidatePublicKey(tokenMintPublicKey, true)
	if err != nil {
		return nil, fmt.Errorf("receiver has an invalid token mint specified: %s: %w", tokenMintPublicKey.String(), err)
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

	bridgeRequestIx, err := skyline_program.NewBridgeRequestInstruction(
		receiver.TokenAmount.Amount,
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
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build bridge request instruction: %w", err)
	}

	return withProgramID(bridgeRequestIx, programID)
}

func (txSnd *TxSender) buildBridgeTransactionInstruction(tx BridgeTransactionDto) ([]solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

	if err := wallet.ValidateAddress(tx.SenderAddr, false); err != nil {
		return nil, fmt.Errorf("invalid sender address: %s: %w", tx.SenderAddr, err)
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.SenderAddr, err)
	}

	if err = txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithValidatorSetPDA(),
		WithVaultPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	registeredTokens, err := txSnd.GetAllRegisteredTokens(tx.Ctx, programID)
	if err != nil {
		return nil, fmt.Errorf("failed to get all registered tokens: %w", err)
	}

	// Mints and transfers must mirror the dedup/index assignment used on-chain
	// (see `derive_bridge_data` in bridge_transaction.rs) so `remaining_accounts`
	// lines up with what the program derives from the validator-signed payload.
	mints, tokenIDs, transferItems, err := deriveBridgeMintsAndTransfers(tx.Receivers, registeredTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to derive bridge mints/transfers: %w", err)
	}

	bridgeTxIx, err := skyline_program.NewBridgeTransactionInstruction(
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		txSnd.instructionConfig.vaultPDA,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.splAssociatedTokenAccountProgramID,
		solana.SysVarInstructionsPubkey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build bridge transaction instruction: %w", err)
	}

	remainingAccounts, err := txSnd.bridgeTransactionRemainingAccounts(programID, mints, tokenIDs, transferItems)
	if err != nil {
		return nil, fmt.Errorf("failed to build bridge transaction remaining accounts: %w", err)
	}

	data, err := bridgeTxIx.Data()
	if err != nil {
		return nil, fmt.Errorf("failed to get instruction data: %w", err)
	}

	bridgeTxIxFinal := solana.NewInstruction(
		programID, append(bridgeTxIx.Accounts(), remainingAccounts...), data)

	batchedEd25519Ix, err := txSnd.buildBatchedEd25519Instruction(tx.SignaturePairs, tx.PayloadBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to build batched ed25519 instruction: %w", err)
	}

	instructions := []solana.Instruction{batchedEd25519Ix, bridgeTxIxFinal}

	// If the transaction has the maximum number of receivers,
	// we need to add a compute budget instruction to increase the compute unit limit for this transaction.
	// This is necessary because transactions with many receivers can require
	// more compute units than the default limit allows.
	if len(tx.Receivers) == MaxBridgeTransactionReceivers {
		computeBudgetIx, err := computebudget.NewSetComputeUnitLimitInstruction(
			BridgeTransactionMaxReceiversComputeUnitLimit,
		).ValidateAndBuild()
		if err != nil {
			return nil, fmt.Errorf("failed to build compute unit limit instruction: %w", err)
		}

		instructions = append([]solana.Instruction{computeBudgetIx}, instructions...)
	}

	return instructions, nil
}

// deriveBridgeMintsAndTransfers builds the dedup'd mint list and per-receiver
// transfer items in the same first-seen order the on-chain program assigns
// `mint_index` values, so account ordering stays consistent.
func deriveBridgeMintsAndTransfers(
	receivers []PayloadReceiver,
	registeredTokens map[uint16]skyline_program.TokenRegistry,
) ([]solana.PublicKey, []uint16, []TransferItem, error) {
	mintIndex := make(map[solana.PublicKey]uint8)

	var (
		mints    []solana.PublicKey
		tokenIDs []uint16
	)

	transferItems := make([]TransferItem, 0, len(receivers))

	for _, receiver := range receivers {
		receiverAddr := solana.PublicKeyFromBytes(receiver.Address[:])

		if err := wallet.ValidateAddress(receiverAddr.String(), false); err != nil {
			return nil, nil, nil, fmt.Errorf("invalid receiver address: %s: %w", receiver.Address, err)
		}

		mint := registeredTokens[receiver.TokenAmount.TokenID].Mint

		idx, exists := mintIndex[mint]
		if !exists {
			idx = uint8(len(mints)) //nolint:gosec // bounded by protocol
			mintIndex[mint] = idx

			mints = append(mints, mint)
			tokenIDs = append(tokenIDs, receiver.TokenAmount.TokenID)
		}

		transferItems = append(transferItems, TransferItem{
			Recipient: receiverAddr,
			MintIndex: idx,
			Amount:    receiver.TokenAmount.Amount,
		})
	}

	return mints, tokenIDs, transferItems, nil
}

func withProgramID(instruction solana.Instruction, programID solana.PublicKey) (solana.Instruction, error) {
	data, err := instruction.Data()
	if err != nil {
		return nil, fmt.Errorf("failed to get instruction data: %w", err)
	}

	return solana.NewInstruction(programID, instruction.Accounts(), data), nil
}

func requireBridgeProgramID(programID solana.PublicKey) error {
	if programID == (solana.PublicKey{}) {
		return fmt.Errorf("bridge program ID is required")
	}

	return nil
}

func (txSnd *TxSender) bridgeTransactionRemainingAccounts(
	programID solana.PublicKey,
	mints []solana.PublicKey,
	tokenIDs []uint16,
	transfers []TransferItem,
) ([]*solana.AccountMeta, error) {
	if len(tokenIDs) != len(mints) {
		return nil, fmt.Errorf("token ID count %d does not match mint count %d", len(tokenIDs), len(mints))
	}

	nMint, nXfer := len(mints), len(transfers)
	out := make([]*solana.AccountMeta, 0, nMint*3+nXfer*2)

	for _, m := range mints {
		out = append(out, solana.NewAccountMeta(m, true, false))
	}

	for _, tr := range transfers {
		out = append(out, solana.NewAccountMeta(tr.Recipient, false, false))
	}

	for i := range mints {
		tokenIDBytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(tokenIDBytes, tokenIDs[i])

		reg, _, err := solana.FindProgramAddress([][]byte{skyline_program.TOKEN_REGISTRY_SEED, tokenIDBytes},
			programID)
		if err != nil {
			return nil, err
		}

		out = append(out, solana.NewAccountMeta(reg, false, false))
	}

	for _, tr := range transfers {
		mintPk := mints[tr.MintIndex]

		ata, _, err := solana.FindAssociatedTokenAddress(tr.Recipient, mintPk)
		if err != nil {
			return nil, err
		}

		out = append(out, solana.NewAccountMeta(ata, true, false))
	}

	for _, m := range mints {
		vaultAta, _, err := solana.FindAssociatedTokenAddress(txSnd.instructionConfig.vaultPDA, m)
		if err != nil {
			return nil, err
		}

		out = append(out, solana.NewAccountMeta(vaultAta, true, false))
	}

	return out, nil
}

// buildBatchedEd25519Instruction constructs a Solana Ed25519 program instruction for batch signature verification.
// This function enables efficient batch verification of multiple Ed25519 signatures against a shared "batchID" message.
// Here's how it works step by step:
//  1. It first ensures there is at least one signature pair provided.
//     Each pair consists of a public key and its corresponding signature.
//  2. The batchID is converted into a little-endian byte representation to be used as the common signed message.
//  3. The serialized instruction data is prepared, sized for the header,
//     per-signature offsets, all pubkeys/signatures, and the shared message.
//  4. For each (pubkey, signature) pair, it encodes an offset struct which specifies where the signature,
//     public key, and message appear in the instruction data (per the ed25519 program spec),
//     referencing the "current instruction" as the data source in the transaction.
//  5. The payload for each signer—32-byte public key followed by 64-byte signature—is appended in sequence.
//  6. The shared message bytes are appended only once at the end of the payload.
//     Each offset struct points to the same message region.
//  7. Finally, the function packages everything into a Solana instruction using the Ed25519 verification program,
//     with no account metas (pure verification).
//
// This design allows Solana to verify all provided signatures at once in a single instruction,
// referencing a common message, and is crucial for ensuring atomic and efficient batch validation
// for bridging operations (such as verifying that a group of validators have signed a batch with a known batch ID).
//
// Example layout of resulting data (for 2 signatures, batchID is 8 bytes):
//
//	[header | offsets | pubkey1 | sig1 | pubkey2 | sig2 | message]
//
// Where:
//   - header:         [num_signatures (1 byte), padding (1 byte)]
//   - offsets:        14 bytes per signature, describes where each pubkey, sig, and message start in the data
//   - pubkeyN:        32 bytes per public key
//   - sigN:           64 bytes per signature
//   - message:        (typically 8 bytes, i.e., batchID in little endian)
//
// Example hex layout for 2 signatures (pubkey1/pk2, sig1/sig2, message):
// [01 00][offsets1...14B][offsets2...14B][pubkey1...32B][sig1...64B][pubkey2...32B][sig2...64B][message...8B]
// That is: [header][offset structs...][payload: pk1][sig1][pk2][sig2][message]
func (txSnd *TxSender) buildBatchedEd25519Instruction(
	signaturePairs map[solana.PublicKey]solana.Signature,
	payloadBytes []byte,
) (solana.Instruction, error) {
	numSigs := len(signaturePairs)
	if numSigs == 0 {
		return nil, fmt.Errorf("no signature pairs provided")
	}

	if len(payloadBytes) == 0 {
		return nil, fmt.Errorf("payload bytes is empty")
	}

	const (
		PubKeyLength            = 32
		SigLength               = 64
		CurrentInstructionIndex = 0xFFFF

		Ed25519ProgramOffsetsSize = 14 // 7 * u16
		Ed25519ProgramHeaderSize  = 2  // num signatures + padding
	)

	data := make([]byte, 0,
		Ed25519ProgramHeaderSize+numSigs*Ed25519ProgramOffsetsSize+numSigs*(PubKeyLength+SigLength)+len(payloadBytes))
	data = append(data, byte(numSigs), 0)

	baseOffset := Ed25519ProgramHeaderSize + numSigs*Ed25519ProgramOffsetsSize
	pubkeySigSectionSize := numSigs * (PubKeyLength + SigLength)
	sharedMsgOffset := baseOffset + pubkeySigSectionSize
	payloadOffset := baseOffset
	payload := make([]byte, 0, pubkeySigSectionSize+len(payloadBytes))

	for pubkey, signature := range signaturePairs {
		publicKeyOffset := payloadOffset
		signatureOffset := publicKeyOffset + PubKeyLength
		offsets := make([]byte, Ed25519ProgramOffsetsSize)
		binary.LittleEndian.PutUint16(offsets[0:], uint16(signatureOffset)) //nolint:gosec // signature offset
		// Reference "current instruction" to support any transaction position.
		binary.LittleEndian.PutUint16(offsets[2:], CurrentInstructionIndex)    // padding
		binary.LittleEndian.PutUint16(offsets[4:], uint16(publicKeyOffset))    //nolint:gosec // public key offset
		binary.LittleEndian.PutUint16(offsets[6:], CurrentInstructionIndex)    // padding
		binary.LittleEndian.PutUint16(offsets[8:], uint16(sharedMsgOffset))    //nolint:gosec // shared message offset
		binary.LittleEndian.PutUint16(offsets[10:], uint16(len(payloadBytes))) //nolint:gosec // message length
		binary.LittleEndian.PutUint16(offsets[12:], CurrentInstructionIndex)   // padding
		data = append(data, offsets...)

		payload = append(payload, pubkey[:]...)
		payload = append(payload, signature[:]...)

		payloadOffset += PubKeyLength + SigLength
	}

	// Append the message only once; every signer entry points to this shared region.
	payload = append(payload, payloadBytes...)
	data = append(data, payload...)

	return solana.NewInstruction(
		Ed25519ProgramID,
		solana.AccountMetaSlice{},
		data,
	), nil
}

func (txSnd *TxSender) buildBridgeVSUInstruction(tx BridgeVSUDto) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

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

	if err = txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithValidatorSetPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	bridgeVSUIx, err := skyline_program.NewBridgeVsuInstruction(
		addingValidatorPubKeys,
		removingValidatorPubKeys,
		tx.BatchID,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		txSnd.instructionConfig.systemProgramID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build bridge VSU instruction: %w", err)
	}

	return withProgramID(bridgeVSUIx, programID)
}

func (txSnd *TxSender) buildHotWalletIncrementInstruction(tx HotWalletIncrementDto) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

	if tx.Amount == 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}

	if err := wallet.ValidateAddress(tx.SenderAddr, true); err != nil {
		return nil, fmt.Errorf("invalid sender address: %s: %w", tx.SenderAddr, err)
	}

	senderPubKey, err := wallet.PublicKeyFromAddress(tx.SenderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sender public key from address %s: %w", tx.SenderAddr, err)
	}

	tokenMintPublicKey, err := wallet.PublicKeyFromAddress(tx.TokenMint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token mint address: %s: %w", tx.TokenMint, err)
	}

	if tokenMintPublicKey != skyline_program.NATIVE_SOL_MINT {
		err = wallet.ValidatePublicKey(tokenMintPublicKey, true)
		if err != nil {
			return nil, fmt.Errorf("invalid token mint specified: %s: %w", tokenMintPublicKey.String(), err)
		}
	}

	if err = txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithVaultPDA(),
		WithTokenRegistryPDA(tx.TokenID),
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

	hotWalletIncrementIx, err := skyline_program.NewHotWalletIncrementInstruction(
		tx.Amount,
		senderPubKey,
		senderAta,
		txSnd.instructionConfig.vaultPDA,
		vaultAta,
		tokenMintPublicKey,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.splAssociatedTokenAccountProgramID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build hot wallet increment instruction: %w", err)
	}

	return withProgramID(hotWalletIncrementIx, programID)
}

// GetProgramConfig loads the global ProgramConfig PDA (version / deploy metadata) from the cluster.
// The tx provider must implement GetAccountInfo (e.g. wallet.Provider).
func (txSnd *TxSender) GetProgramConfig(
	ctx context.Context,
	programID solana.PublicKey,
) (*skyline_program.ProgramConfig, error) {
	if err := requireBridgeProgramID(programID); err != nil {
		return nil, err
	}

	programConfigPDA, _, err := solana.FindProgramAddress([][]byte{skyline_program.PROGRAM_CONFIG_SEED}, programID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive program config PDA: %w", err)
	}

	info, err := txSnd.txProvider.GetAccountInfo(ctx, programConfigPDA)
	if err != nil {
		return nil, fmt.Errorf("get program config account: %w", err)
	}

	if info == nil || info.Value == nil || info.Value.Data == nil {
		return nil, fmt.Errorf("program config account not found at %s", programConfigPDA.String())
	}

	raw := info.Value.Data.GetBinary()
	if len(raw) == 0 {
		return nil, fmt.Errorf("program config account at %s has empty data", programConfigPDA.String())
	}

	cfg, err := skyline_program.ParseAccount_ProgramConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("decode program config: %w", err)
	}

	return cfg, nil
}

// GetAllRegisteredTokens loads all TokenRegistry accounts for the bridge program.
// The tx provider must implement GetProgramAccounts (e.g. wallet.Provider).
func (txSnd *TxSender) GetAllRegisteredTokens(
	ctx context.Context,
	programID solana.PublicKey,
) (map[uint16]skyline_program.TokenRegistry, error) {
	if err := requireBridgeProgramID(programID); err != nil {
		return nil, err
	}

	filters := []rpc.RPCFilter{
		{
			Memcmp: &rpc.RPCFilterMemcmp{
				Offset: 0,
				Bytes:  solana.Base58(skyline_program.Account_TokenRegistry[:]),
			},
		},
	}

	accounts, err := txSnd.txProvider.GetProgramAccounts(
		ctx,
		programID,
		&rpc.GetProgramAccountsOpts{
			Commitment: rpc.CommitmentConfirmed,
			Encoding:   solana.EncodingBase64,
			Filters:    filters,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("get token registry accounts: %w", err)
	}

	registeredTokens := make(map[uint16]skyline_program.TokenRegistry, len(accounts))

	for _, account := range accounts {
		if account == nil {
			return nil, fmt.Errorf("token registry account entry is nil")
		}

		if account.Account == nil || account.Account.Data == nil {
			return nil, fmt.Errorf("token registry account %s has empty data", account.Pubkey.String())
		}

		raw := account.Account.Data.GetBinary()
		if len(raw) == 0 {
			return nil, fmt.Errorf("token registry account %s has empty data", account.Pubkey.String())
		}

		registry, err := skyline_program.ParseAccount_TokenRegistry(raw)
		if err != nil {
			return nil, fmt.Errorf("decode token registry %s: %w", account.Pubkey.String(), err)
		}

		registeredTokens[registry.TokenId] = *registry
	}

	return registeredTokens, nil
}

func (txSnd *TxSender) buildInitializeInstruction(tx InitializeDto) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

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
		WithProgramID(programID),
		WithProgramDataAccount(),
		WithValidatorSetPDA(),
		WithVaultPDA(),
		WithFeeConfigPDA(),
		WithProgramConfigPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	initializeIx, err := skyline_program.NewInitializeInstruction(
		validatorPubKeys,
		&tx.LastID,
		txSnd.chainConfig.MinOperationFeeAmount,
		txSnd.chainConfig.MinFeeForBridging,
		senderPubKey,
		txSnd.instructionConfig.validatorSetPDA,
		txSnd.instructionConfig.vaultPDA,
		txSnd.instructionConfig.feeConfigPDA,
		txSnd.instructionConfig.programConfigPDA,
		txSnd.chainConfig.TreasuryAddress,
		txSnd.instructionConfig.programKey,
		txSnd.instructionConfig.programDataKey,
		txSnd.instructionConfig.systemProgramID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build initialize instruction: %w", err)
	}

	return withProgramID(initializeIx, programID)
}

func (txSnd *TxSender) buildRegisterTokenLockUnlockInstruction(
	tx RegisterTokenLockUnlockDto) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

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
		WithProgramID(programID),
		WithFeeConfigPDA(),
		WithTokenRegistryPDA(tx.TokenID),
		WithTokenIDGuardPDA(tx.TokenID),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	registerLockUnlockIx, err := skyline_program.NewRegisterLockUnlockTokenInstruction(
		tx.TokenID,
		tx.MinBridgingAmount,
		authorityPubKey,
		txSnd.instructionConfig.feeConfigPDA,
		tokenMintPublicKey,
		txSnd.instructionConfig.tokenRegistryPDA,
		txSnd.instructionConfig.tokenIDGuardPDA,
		txSnd.instructionConfig.systemProgramID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build register lock/unlock token instruction: %w", err)
	}

	return withProgramID(registerLockUnlockIx, programID)
}

func (txSnd *TxSender) buildRegisterTokenMintBurnInstruction(tx RegisterTokenMintBurnDto) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

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
		WithProgramID(programID),
		WithFeeConfigPDA(),
		WithVaultPDA(),
		WithMetadataPDA(tokenMintPublicKey),
		WithTokenRegistryPDA(tx.TokenID),
		WithTokenIDGuardPDA(tx.TokenID),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	registerMintBurnIx, err := skyline_program.NewRegisterMintBurnTokenInstruction(
		tx.TokenID,
		tx.Decimals,
		tx.MinBridgingAmount,
		tx.Name,
		tx.Symbol,
		tx.URI,
		authorityPubKey,
		txSnd.instructionConfig.feeConfigPDA,
		txSnd.instructionConfig.vaultPDA,
		tokenMintPublicKey,
		txSnd.instructionConfig.metadataPDA,
		txSnd.instructionConfig.tokenRegistryPDA,
		txSnd.instructionConfig.tokenIDGuardPDA,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.tokenMetadataProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.rentPubkey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build register mint/burn token instruction: %w", err)
	}

	return withProgramID(registerMintBurnIx, programID)
}

func (txSnd *TxSender) buildUpdateFeeConfigInstruction(tx UpdateFeeConfigDto) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

	if err := wallet.ValidateAddress(tx.AuthorityAddr, true); err != nil {
		return nil, fmt.Errorf("invalid authority address: %s: %w", tx.AuthorityAddr, err)
	}

	authorityPubKey, err := wallet.PublicKeyFromAddress(tx.AuthorityAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse authority public key from address %s: %w", tx.AuthorityAddr, err)
	}

	newTreasuryAcc := solana.PublicKey{}

	if tx.UpdateTreasury && tx.NewTreasuryAddress != "" {
		if err := wallet.ValidateAddress(tx.NewTreasuryAddress, true); err != nil {
			return nil, fmt.Errorf("invalid new treasury address: %s: %w", tx.NewTreasuryAddress, err)
		}

		newTreasuryAcc, err = wallet.PublicKeyFromAddress(tx.NewTreasuryAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to parse new treasury public key from address %s: %w", tx.NewTreasuryAddress, err)
		}
	}

	if err = txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithFeeConfigPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	updateFeeConfigIx, err := skyline_program.NewUpdateFeeConfigInstruction(
		&tx.MinOperationFee,
		&tx.BridgingFee,
		&tx.UpdateTreasury,
		authorityPubKey,
		txSnd.instructionConfig.feeConfigPDA,
		newTreasuryAcc,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build update fee config instruction: %w", err)
	}

	return withProgramID(updateFeeConfigIx, programID)
}

func (txSnd *TxSender) buildUpdateProgramVersionInstruction(
	tx UpdateProgramVersionDto,
) (solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

	if err := wallet.ValidateAddress(tx.AuthorityAddr, true); err != nil {
		return nil, fmt.Errorf("invalid authority address: %s: %w", tx.AuthorityAddr, err)
	}

	authorityPubKey, err := wallet.PublicKeyFromAddress(tx.AuthorityAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse authority public key from address %s: %w", tx.AuthorityAddr, err)
	}

	if err := txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithProgramConfigPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	updateIx, err := skyline_program.NewUpdateProgramVersionInstruction(
		tx.VersionString,
		authorityPubKey,
		txSnd.instructionConfig.programConfigPDA,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build update program version instruction: %w", err)
	}

	return withProgramID(updateIx, programID)
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

func (txSnd *TxSender) buildUpdateMinBridgingAmountInstruction(tx UpdateMinBridgingAmountDto) (
	solana.Instruction, error) {
	if err := requireBridgeProgramID(tx.ProgramID); err != nil {
		return nil, err
	}

	programID := tx.ProgramID

	if err := wallet.ValidateAddress(tx.AuthorityAddr, true); err != nil {
		return nil, fmt.Errorf("invalid authority address: %s: %w", tx.AuthorityAddr, err)
	}

	authorityPubKey, err := wallet.PublicKeyFromAddress(tx.AuthorityAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse authority public key from address %s: %w", tx.AuthorityAddr, err)
	}

	if err := txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithFeeConfigPDA(),
		WithTokenRegistryPDA(tx.TokenID),
	); err != nil {
		return nil, fmt.Errorf("failed to apply additional config options: %w", err)
	}

	updateMinBridgingAmountIx, err := skyline_program.NewUpdateMinBridgingAmountInstruction(
		tx.TokenID,
		tx.MinBridgingAmount,
		authorityPubKey,
		txSnd.instructionConfig.feeConfigPDA,
		txSnd.instructionConfig.tokenRegistryPDA,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build update min bridging amount instruction: %w", err)
	}

	return withProgramID(updateMinBridgingAmountIx, programID)
}
