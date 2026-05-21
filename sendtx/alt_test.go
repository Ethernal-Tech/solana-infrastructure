package sendtx

import (
	"encoding/binary"
	"testing"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/require"
)

func TestBridgeTransactionALTAddresses_NoMints(t *testing.T) {
	txSender := NewTxSender(
		new(wallet.MockTxProvider),
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
		new(wallet.MockTxProvider),
		&ChainConfig{},
	)

	mints := map[uint16]solana.PublicKey{
		1: solana.NewWallet().PublicKey(),
		2: solana.NewWallet().PublicKey(),
	}

	got, err := txSender.BridgeTransactionALTAddresses(skyline_program.ProgramID, mints)
	require.NoError(t, err)
	require.Len(t, got, 6+3*len(mints))

	vaultPDA, _, err := solana.FindProgramAddress(
		[][]byte{skyline_program.VAULT_SEED}, skyline_program.ProgramID)
	require.NoError(t, err)
	require.Equal(t, vaultPDA, got[1])

	for i, tokenID := range []uint16{1, 2} {
		mint := mints[tokenID]
		base := 6 + i*3

		tokenIDBytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(tokenIDBytes, tokenID)

		expectedRegistry, _, err := solana.FindProgramAddress(
			[][]byte{skyline_program.TOKEN_REGISTRY_SEED, tokenIDBytes}, skyline_program.ProgramID)
		require.NoError(t, err)

		expectedVaultATA, _, err := wallet.FindAssociatedTokenAddress(vaultPDA, mint)
		require.NoError(t, err)

		require.Equal(t, mint, got[base], "mint order must follow token ID order")
		require.Equal(t, expectedRegistry, got[base+1])
		require.Equal(t, expectedVaultATA, got[base+2])
	}
}

func TestBridgeTransactionALTAddresses_SortsMapByTokenID(t *testing.T) {
	txSender := NewTxSender(
		new(wallet.MockTxProvider),
		&ChainConfig{},
	)

	mint7 := solana.NewWallet().PublicKey()
	mint42 := solana.NewWallet().PublicKey()
	mint100 := solana.NewWallet().PublicKey()

	got, err := txSender.BridgeTransactionALTAddresses(
		skyline_program.ProgramID,
		map[uint16]solana.PublicKey{
			42:  mint42,
			100: mint100,
			7:   mint7,
		},
	)
	require.NoError(t, err)
	require.Len(t, got, 15)

	require.Equal(t, mint7, got[6])
	require.Equal(t, mint42, got[9])
	require.Equal(t, mint100, got[12])
}

func TestBridgeTransactionALTAddresses_RegistryUsesTokenIDNotMint(t *testing.T) {
	txSender := NewTxSender(
		new(wallet.MockTxProvider),
		&ChainConfig{},
	)

	mint := solana.NewWallet().PublicKey()

	got, err := txSender.BridgeTransactionALTAddresses(
		skyline_program.ProgramID,
		map[uint16]solana.PublicKey{
			1: mint,
			2: mint,
		},
	)
	require.NoError(t, err)
	require.Len(t, got, 12)

	require.Equal(t, mint, got[6])
	require.Equal(t, expectedTokenRegistryPDA(t, 1), got[7])
	require.Equal(t, mint, got[9])
	require.Equal(t, expectedTokenRegistryPDA(t, 2), got[10])
	require.NotEqual(t, got[7], got[10], "registry PDA must be keyed by token ID")
}

func TestBridgeTransactionALTAddresses_ExcludesSenderAndReceiver(t *testing.T) {
	txSender := NewTxSender(
		new(wallet.MockTxProvider),
		&ChainConfig{},
	)

	mint := solana.NewWallet().PublicKey()
	sender := solana.NewWallet().PublicKey()
	receiver := solana.NewWallet().PublicKey()

	got, err := txSender.BridgeTransactionALTAddresses(skyline_program.ProgramID, map[uint16]solana.PublicKey{1: mint})
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

func TestBridgeTransactionALTAddresses_SkipsNativeSolMint(t *testing.T) {
	txSender := NewTxSender(
		new(wallet.MockTxProvider),
		&ChainConfig{},
	)

	got, err := txSender.BridgeTransactionALTAddresses(
		skyline_program.ProgramID,
		map[uint16]solana.PublicKey{
			1: skyline_program.NATIVE_SOL_MINT,
		},
	)
	require.NoError(t, err)
	require.Len(t, got, 6, "native SOL does not need per-mint SPL accounts")
}

// TestBridgeTransactionALTAddresses_RoundTripExtend proves the common flow:
// feed the helper's output into ALTAdmin.NewExtendInstructions with a
// partially-populated ALT and get back an instruction that carries only the
// missing entries.
func TestBridgeTransactionALTAddresses_RoundTripExtend(t *testing.T) {
	txSender := NewTxSender(
		new(wallet.MockTxProvider),
		&ChainConfig{},
	)

	oldMint := solana.NewWallet().PublicKey()
	newMint := solana.NewWallet().PublicKey()

	oldSet, err := txSender.BridgeTransactionALTAddresses(
		skyline_program.ProgramID, map[uint16]solana.PublicKey{1: oldMint})
	require.NoError(t, err)

	require.Len(t, oldSet, 9)

	fullSet, err := txSender.BridgeTransactionALTAddresses(
		skyline_program.ProgramID, map[uint16]solana.PublicKey{1: oldMint, 2: newMint})
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

func expectedTokenRegistryPDA(t *testing.T, tokenID uint16) solana.PublicKey {
	t.Helper()

	tokenIDBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(tokenIDBytes, tokenID)

	pda, _, err := solana.FindProgramAddress(
		[][]byte{skyline_program.TOKEN_REGISTRY_SEED, tokenIDBytes},
		skyline_program.ProgramID,
	)
	require.NoError(t, err)

	return pda
}
