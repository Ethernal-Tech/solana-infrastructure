package sendtx

import (
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
)

func TestBridgeTransactionALTAddresses_NoMints(t *testing.T) {
	txSender := NewTxSender(
		new(MockTxSubmiter),
		&ChainConfig{},
	)

	got, err := txSender.BridgeTransactionALTAddresses(skyline_program.ProgramID, nil)
	require.NoError(t, err)
	require.Len(t, got, 6, "only the 6 global keys should be returned for no mints")

	expectedValidatorSet, _, err := solana.FindProgramAddress(
		[][]byte{skyline_program.VALIDATOR_SET_SEED}, skyline_program.ProgramID)
	require.NoError(t, err)

	expectedVault, _, err := solana.FindProgramAddress(
		[][]byte{skyline_program.VAULT_SEED}, skyline_program.ProgramID)
	require.NoError(t, err)

	require.Equal(t, expectedValidatorSet, got[0])
	require.Equal(t, expectedVault, got[1])
	require.Equal(t, solana.TokenProgramID, got[2])
	require.Equal(t, solana.SystemProgramID, got[3])
	require.Equal(t, solana.SPLAssociatedTokenAccountProgramID, got[4])
	require.Equal(t, solana.SysVarInstructionsPubkey, got[5])
}

func TestBridgeTransactionALTAddresses_WithMints(t *testing.T) {
	txSender := NewTxSender(
		new(MockTxSubmiter),
		&ChainConfig{},
	)

	mints := []solana.PublicKey{
		solana.NewWallet().PublicKey(),
		solana.NewWallet().PublicKey(),
	}

	got, err := txSender.BridgeTransactionALTAddresses(skyline_program.ProgramID, mints)
	require.NoError(t, err)
	require.Len(t, got, 6+3*len(mints))

	vaultPDA, _, err := solana.FindProgramAddress(
		[][]byte{skyline_program.VAULT_SEED}, skyline_program.ProgramID)
	require.NoError(t, err)
	require.Equal(t, vaultPDA, got[1])

	for i, mint := range mints {
		base := 6 + i*3

		expectedRegistry, _, err := solana.FindProgramAddress(
			[][]byte{skyline_program.TOKEN_REGISTRY_SEED, mint[:]}, skyline_program.ProgramID)
		require.NoError(t, err)

		expectedVaultATA, _, err := wallet.FindAssociatedTokenAddress(vaultPDA, mint)
		require.NoError(t, err)

		require.Equal(t, mint, got[base], "mint order must match input order")
		require.Equal(t, expectedRegistry, got[base+1])
		require.Equal(t, expectedVaultATA, got[base+2])
	}
}

func TestBridgeTransactionALTAddresses_ExcludesSenderAndReceiver(t *testing.T) {
	txSender := NewTxSender(
		new(MockTxSubmiter),
		&ChainConfig{},
	)

	mint := solana.NewWallet().PublicKey()
	sender := solana.NewWallet().PublicKey()
	receiver := solana.NewWallet().PublicKey()

	got, err := txSender.BridgeTransactionALTAddresses(skyline_program.ProgramID, []solana.PublicKey{mint})
	require.NoError(t, err)

	receiverATA, _, err := wallet.FindAssociatedTokenAddress(receiver, mint)
	require.NoError(t, err)

	for _, key := range got {
		require.NotEqual(t, sender, key, "sender key must not appear in ALT set")
		require.NotEqual(t, receiver, key, "receiver key must not appear in ALT set")
		require.NotEqual(t, receiverATA, key, "receiver ATA must not appear in ALT set")
		require.NotEqual(t, skyline_program.ProgramID, key,
			"skyline program ID must stay in static keys")
		require.NotEqual(t, Ed25519ProgramID, key,
			"ed25519 program ID must stay in static keys")
	}
}

func TestBridgeTransactionALTAddresses_RejectsZeroMint(t *testing.T) {
	txSender := NewTxSender(
		new(MockTxSubmiter),
		&ChainConfig{},
	)

	_, err := txSender.BridgeTransactionALTAddresses(skyline_program.ProgramID, []solana.PublicKey{{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid mint")
}

// TestBridgeTransactionALTAddresses_RoundTripExtend proves the common flow:
// feed the helper's output into ALTAdmin.NewExtendInstructions with a
// partially-populated ALT and get back an instruction that carries only the
// missing entries.
func TestBridgeTransactionALTAddresses_RoundTripExtend(t *testing.T) {
	txSender := NewTxSender(
		new(MockTxSubmiter),
		&ChainConfig{},
	)

	oldMint := solana.NewWallet().PublicKey()
	newMint := solana.NewWallet().PublicKey()

	oldSet, err := txSender.BridgeTransactionALTAddresses(
		skyline_program.ProgramID, []solana.PublicKey{oldMint})
	require.NoError(t, err)

	require.Len(t, oldSet, 9)

	fullSet, err := txSender.BridgeTransactionALTAddresses(
		skyline_program.ProgramID, []solana.PublicKey{oldMint, newMint})
	require.NoError(t, err)

	require.Len(t, fullSet, 12)

	seenOld := make(map[solana.PublicKey]struct{}, len(oldSet))
	for _, k := range oldSet {
		seenOld[k] = struct{}{}
	}

	var newOnly []solana.PublicKey

	for _, k := range fullSet {
		if _, ok := seenOld[k]; !ok {
			newOnly = append(newOnly, k)
		}
	}

	require.Len(t, newOnly, 3,
		"only the 3 per-mint keys for the newly added mint should be missing from oldSet")
	require.Contains(t, newOnly, newMint)
}
