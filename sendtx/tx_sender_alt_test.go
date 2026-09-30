package sendtx

import (
	"context"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	computebudget "github.com/gagliardetto/solana-go/programs/compute-budget"
	"github.com/stretchr/testify/require"
)

// TestBridgingTransaction_WithAddressLookupTables proves that passing a
// non-empty ALT map to CreateTx:
//  1. produces a v0 (versioned) transaction,
//  2. populates MessageAddressTableLookups for the provided ALT,
//  3. shrinks the static account keys compared to an ALT-less build,
//  4. round-trips through MarshalTransaction/UnmarshalTransaction.
func TestBridgingTransaction_WithAddressLookupTables(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	const receiverCount = 8

	receivers := make([]PayloadReceiver, receiverCount)

	for i := 0; i < receiverCount; i++ {
		receivers[i] = PayloadReceiver{
			Address: solana.NewWallet().PublicKey(),
			TokenAmount: wallet.TokenAmount{
				Amount:  1000,
				TokenID: 1,
			},
		}
	}

	signaturePairs := map[solana.PublicKey]solana.Signature{
		solana.NewWallet().PublicKey(): {1, 2, 3},
	}

	txDto := BridgeTransactionDto{
		ProgramID:      skyline_program.ProgramID,
		SenderAddr:     senderPrivateKey.PublicKey().String(),
		Receivers:      receivers,
		PayloadBytes:   []byte{1, 2, 3, 4},
		SignaturePairs: signaturePairs,
	}

	mockProvider := new(wallet.MockTxProvider)
	mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

	txSender := NewTxSender(
		mockProvider,
		&ChainConfig{
			TreasuryAddress: treasuryWallet.PublicKey,
		},
	)

	recentBlockHash := solana.Hash{7}

	baseline, err := txSender.CreateTx(
		ctx,
		senderPrivateKey.PublicKey(),
		InstructionTypeBridgeTransaction,
		recentBlockHash,
		txDto,
	)
	require.NoError(t, err)
	require.Equal(t, solana.MessageVersionV1, baseline.Message.GetVersion(),
		"a batch built without ALTs should be v1")

	altAddr := solana.NewWallet().PublicKey()
	altEntries := accountsEligibleForALT(t, baseline, senderPrivateKey.PublicKey())
	require.NotEmpty(t, altEntries, "expected at least one ALT-eligible account")

	tx, err := txSender.CreateTx(
		ctx,
		senderPrivateKey.PublicKey(),
		InstructionTypeBridgeTransaction,
		recentBlockHash,
		txDto,
		WithAddressLookupTables(map[solana.PublicKey]solana.PublicKeySlice{
			altAddr: altEntries,
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, tx)

	require.True(t, tx.Message.IsVersioned(), "tx should be v0 when ALTs are used")
	require.Equal(t, solana.MessageVersionV0, tx.Message.GetVersion())

	lookups := tx.Message.GetAddressTableLookups()
	require.Len(t, lookups, 1)
	require.Equal(t, altAddr, lookups[0].AccountKey)
	require.NotEmpty(t,
		append(append([]uint8{}, lookups[0].WritableIndexes...), lookups[0].ReadonlyIndexes...),
		"at least one account should be resolved through the ALT")

	require.Less(t, len(tx.Message.AccountKeys), len(baseline.Message.AccountKeys),
		"ALT-using tx should have fewer static account keys")

	raw, err := wallet.MarshalTransaction(tx)
	require.NoError(t, err)

	decoded, err := wallet.UnmarshalTransaction(raw)
	require.NoError(t, err)
	require.True(t, decoded.Message.IsVersioned())
	require.Equal(t, tx.Message.RecentBlockhash, decoded.Message.RecentBlockhash)
	require.Equal(t, tx.Message.Instructions, decoded.Message.Instructions)
	require.Len(t, decoded.Message.GetAddressTableLookups(), 1)
	require.Equal(t, altAddr, decoded.Message.GetAddressTableLookups()[0].AccountKey)
}

// TestBridgeTransaction_IsV1WithoutALTs makes sure a batch is built as a v1
// (SIMD-0385) message when no ALT is attached: 0x81 on the wire, the compute
// budget inlined in the message header, the 4096-byte limit, and a clean
// marshal/unmarshal round trip.
func TestBridgeTransaction_IsV1WithoutALTs(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	mockProvider := new(wallet.MockTxProvider)
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
		solana.Hash{7},
		bridgeTransactionDto(t, senderPrivateKey, 1),
	)
	require.NoError(t, err)
	require.Equal(t, solana.MessageVersionV1, tx.Message.GetVersion())

	// No ComputeBudget instruction was built, so the limit the runtime would
	// have granted implicitly under v0 is requested explicitly instead.
	require.Len(t, tx.Message.Instructions, 2)
	require.NotNil(t, tx.Message.TransactionConfig.ComputeUnitLimit)
	require.Equal(t,
		DefaultComputeUnitLimitPerInstruction*2, *tx.Message.TransactionConfig.ComputeUnitLimit)
	require.Nil(t, tx.Message.TransactionConfig.PriorityFee)

	// An unset loaded accounts data size limit means 0 bytes in v1, which the
	// first account load exceeds, so it has to be requested too.
	require.NotNil(t, tx.Message.TransactionConfig.LoadedAccountsDataSizeLimit)
	require.Equal(t,
		MaxLoadedAccountsDataSizeBytes, *tx.Message.TransactionConfig.LoadedAccountsDataSizeLimit)

	_, err = tx.Sign(func(solana.PublicKey) *solana.PrivateKey {
		return &senderPrivateKey
	})
	require.NoError(t, err)

	raw, err := wallet.MarshalTransaction(tx)
	require.NoError(t, err)
	require.Equal(t, byte(0x81), raw[0], "v1 transactions start with the 0x81 discriminator")
	require.LessOrEqual(t, len(raw), solana.MaxTransactionSizeV1)

	decoded, err := wallet.UnmarshalTransaction(raw)
	require.NoError(t, err)
	require.Equal(t, solana.MessageVersionV1, decoded.Message.GetVersion())
	require.Equal(t, tx.Message.AccountKeys, decoded.Message.AccountKeys)
	require.Equal(t, tx.Message.Instructions, decoded.Message.Instructions)
	require.Equal(t, tx.Signatures, decoded.Signatures)
	require.Equal(t,
		*tx.Message.TransactionConfig.ComputeUnitLimit,
		*decoded.Message.TransactionConfig.ComputeUnitLimit)
	require.NoError(t, decoded.VerifySignatures())
}

// TestBridgeTransaction_V1InlinesComputeBudget covers the full batch: its
// ComputeBudget instruction has to move into the v1 message header, because a
// ComputeBudget instruction is a no-op in v1 and would leave the batch running
// on 0 compute units.
func TestBridgeTransaction_V1InlinesComputeBudget(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	mockProvider := new(wallet.MockTxProvider)
	mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

	txSender := NewTxSender(
		mockProvider,
		&ChainConfig{
			TreasuryAddress: treasuryWallet.PublicKey,
		},
	)

	txDto := bridgeTransactionDto(t, senderPrivateKey, MaxBridgeTransactionReceivers)

	instructions, err := txSender.buildInstructions(InstructionTypeBridgeTransaction, txDto)
	require.NoError(t, err)
	require.Len(t, instructions, 3, "a full batch carries a ComputeBudget instruction")
	require.True(t, instructions[0].ProgramID().Equals(solana.ComputeBudget))

	tx, err := txSender.CreateTx(
		ctx,
		senderPrivateKey.PublicKey(),
		InstructionTypeBridgeTransaction,
		solana.Hash{7},
		txDto,
	)
	require.NoError(t, err)
	require.Equal(t, solana.MessageVersionV1, tx.Message.GetVersion())

	require.Len(t, tx.Message.Instructions, 2, "the ComputeBudget instruction is not sent")

	for _, ix := range tx.Message.Instructions {
		programID, err := tx.Message.Program(ix.ProgramIDIndex)
		require.NoError(t, err)
		require.False(t, programID.Equals(solana.ComputeBudget))
	}

	require.NotNil(t, tx.Message.TransactionConfig.ComputeUnitLimit)
	require.Equal(t,
		BridgeTransactionMaxReceiversComputeUnitLimit,
		*tx.Message.TransactionConfig.ComputeUnitLimit,
		"the requested limit is carried over unchanged")
}

// TestBridgeTransaction_ALTOptionKeepsV0 documents the one way a batch stays on
// the old format: ALTs exist only in v0, so asking for one opts out of v1.
func TestBridgeTransaction_ALTOptionKeepsV0(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	mockProvider := new(wallet.MockTxProvider)
	mockBridgeTokenRegistry(t, mockProvider, defaultBridgeTokenRegistry(1))

	txSender := NewTxSender(
		mockProvider,
		&ChainConfig{
			TreasuryAddress: treasuryWallet.PublicKey,
		},
	)

	txDto := bridgeTransactionDto(t, senderPrivateKey, MaxBridgeTransactionReceivers)

	tx, err := txSender.CreateTx(
		ctx,
		senderPrivateKey.PublicKey(),
		InstructionTypeBridgeTransaction,
		solana.Hash{7},
		txDto,
		WithAddressLookupTables(map[solana.PublicKey]solana.PublicKeySlice{
			solana.NewWallet().PublicKey(): {solana.NewWallet().PublicKey()},
		}),
	)
	require.NoError(t, err)
	require.Equal(t, solana.MessageVersionV0, tx.Message.GetVersion())

	// On v0 the compute budget stays where it was: an instruction.
	require.Len(t, tx.Message.Instructions, 3)
	require.True(t, tx.Message.TransactionConfig.IsEmpty())
}

// bridgeTransactionDto builds a batch DTO with receiverCount receivers, all on
// token ID 1.
func bridgeTransactionDto(
	t *testing.T, senderPrivateKey solana.PrivateKey, receiverCount int,
) BridgeTransactionDto {
	t.Helper()

	receivers := make([]PayloadReceiver, receiverCount)

	for i := range receivers {
		receivers[i] = PayloadReceiver{
			Address: solana.NewWallet().PublicKey(),
			TokenAmount: wallet.TokenAmount{
				Amount:  1000,
				TokenID: 1,
			},
		}
	}

	return BridgeTransactionDto{
		ProgramID:    skyline_program.ProgramID,
		SenderAddr:   senderPrivateKey.PublicKey().String(),
		PayloadBytes: []byte{1, 2, 3, 4},
		SignaturePairs: map[solana.PublicKey]solana.Signature{
			solana.NewWallet().PublicKey(): {1, 2, 3},
		},
		Receivers: receivers,
	}
}

// TestCreateTx_NoALTOption_KeepsLegacyBehavior makes sure that callers that
// don't pass any option still get a legacy transaction (back-compat).
func TestCreateTx_NoALTOption_KeepsLegacyBehavior(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	txSender := NewTxSender(
		new(wallet.MockTxProvider),
		&ChainConfig{
			TreasuryAddress: treasuryWallet.PublicKey,
		},
	)

	receiverWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	tx, err := txSender.CreateTx(
		ctx,
		senderPrivateKey.PublicKey(),
		InstructionTypeSOLTransfer,
		solana.Hash{},
		SOLTransferDto{
			SenderPublicKey:   senderPrivateKey.PublicKey().String(),
			ReceiverPublicKey: receiverWallet.PublicKey.String(),
			Amount:            1_000,
		},
	)
	require.NoError(t, err)
	require.False(t, tx.Message.IsVersioned())
	require.Empty(t, tx.Message.GetAddressTableLookups())
}

// accountsEligibleForALT returns the subset of a compiled transaction's static
// account keys that are legal to resolve via an Address Lookup Table: any
// account that is neither the fee payer nor a signer, and not the program ID
// of any instruction in the message (program IDs must stay in the static
// keys).
func accountsEligibleForALT(
	t *testing.T, tx *solana.Transaction, feePayer solana.PublicKey,
) solana.PublicKeySlice {
	t.Helper()

	programIDs := map[solana.PublicKey]struct{}{}

	for _, ix := range tx.Message.Instructions {
		pid, err := tx.Message.Program(ix.ProgramIDIndex)
		require.NoError(t, err)

		programIDs[pid] = struct{}{}
	}

	// Any PDA passed to the skyline program that's not a signer/fee-payer is
	// fine to move into an ALT. The builder keeps signers/fee-payer at the
	// head of AccountKeys; everything after the signers section is fair game
	// except for program IDs.
	out := make(solana.PublicKeySlice, 0, len(tx.Message.AccountKeys))

	for _, key := range tx.Message.AccountKeys {
		if key.Equals(feePayer) || tx.Message.IsSigner(key) {
			continue
		}

		if _, isProgram := programIDs[key]; isProgram {
			continue
		}

		out = append(out, key)
	}

	return out
}

// TestInlineComputeBudget_Limits covers the two resource limits a v1 message
// carries in its header. Both have to be present: v1 reads an unset limit as 0,
// not as "use the runtime default", so a missing compute unit limit means no
// compute and a missing loaded accounts data size limit means
// MaxLoadedAccountsDataSizeExceeded on the first account the transaction loads.
func TestInlineComputeBudget_Limits(t *testing.T) {
	t.Run("both limits defaulted when no ComputeBudget instruction is present", func(t *testing.T) {
		instructions := []solana.Instruction{
			solana.NewInstruction(solana.SystemProgramID, nil, nil),
		}

		remaining, config, err := inlineComputeBudget(instructions)
		require.NoError(t, err)
		require.Len(t, remaining, 1)

		require.NotNil(t, config.ComputeUnitLimit)
		require.Equal(t, DefaultComputeUnitLimitPerInstruction, *config.ComputeUnitLimit)

		require.NotNil(t, config.LoadedAccountsDataSizeLimit)
		require.Equal(t, MaxLoadedAccountsDataSizeBytes, *config.LoadedAccountsDataSizeLimit)
	})

	t.Run("explicitly requested limits are carried over, not defaulted", func(t *testing.T) {
		const (
			computeUnitLimit uint32 = 123_456
			dataSizeLimit    uint32 = 96 * 1024
		)

		instructions := []solana.Instruction{
			computebudget.NewSetComputeUnitLimitInstruction(computeUnitLimit).Build(),
			computebudget.NewSetLoadedAccountsDataSizeLimitInstruction(dataSizeLimit).Build(),
			solana.NewInstruction(solana.SystemProgramID, nil, nil),
		}

		remaining, config, err := inlineComputeBudget(instructions)
		require.NoError(t, err)
		require.Len(t, remaining, 1, "the ComputeBudget instructions moved into the header")

		require.NotNil(t, config.ComputeUnitLimit)
		require.Equal(t, computeUnitLimit, *config.ComputeUnitLimit)

		require.NotNil(t, config.LoadedAccountsDataSizeLimit)
		require.Equal(t, dataSizeLimit, *config.LoadedAccountsDataSizeLimit)
	})
}
