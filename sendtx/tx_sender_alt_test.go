package sendtx

import (
	"context"
	"math/big"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
)

// TestBridgingTransaction_WithAddressLookupTables proves that passing a
// non-empty ALT map to CreateTx:
//  1. produces a v0 (versioned) transaction,
//  2. populates MessageAddressTableLookups for the provided ALT,
//  3. shrinks the static account keys compared to a legacy build,
//  4. round-trips through MarshalTransaction/UnmarshalTransaction.
func TestBridgingTransaction_WithAddressLookupTables(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	const receiverCount = 8

	receivers := make([]BridgingTxReceiver, receiverCount)
	mint := solana.NewWallet().PublicKey().String()

	for i := 0; i < receiverCount; i++ {
		receivers[i] = BridgingTxReceiver{
			Address: solana.NewWallet().PublicKey().String(),
			TokenAmount: wallet.TokenAmount{
				Amount:    new(big.Int).SetUint64(1000),
				TokenMint: mint,
			},
		}
	}

	signaturePairs := map[solana.PublicKey]solana.Signature{
		solana.NewWallet().PublicKey(): {1, 2, 3},
	}

	txDto := BridgeTransactionDto{
		SenderAddr:     senderPrivateKey.PublicKey().String(),
		BatchID:        42,
		PayloadBytes:   []byte{1, 2, 3, 4},
		SignaturePairs: signaturePairs,
		Receivers:      receivers,
	}

	txSender := NewTxSender(
		new(MockTxSubmiter),
		&ChainConfig{
			TreasuryAddress:    treasuryWallet.PublicKey,
			BridgingFeeAddress: feeWallet.PublicKey,
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
	require.False(t, baseline.Message.IsVersioned(), "baseline tx should be legacy")

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

// TestCreateTx_NoALTOption_KeepsLegacyBehavior makes sure that callers that
// don't pass any option still get a legacy transaction (back-compat).
func TestCreateTx_NoALTOption_KeepsLegacyBehavior(t *testing.T) {
	ctx := context.Background()

	senderPrivateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	treasuryWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	feeWallet, err := wallet.NewWallet()
	require.NoError(t, err)

	txSender := NewTxSender(
		new(MockTxSubmiter),
		&ChainConfig{
			TreasuryAddress:    treasuryWallet.PublicKey,
			BridgingFeeAddress: feeWallet.PublicKey,
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
