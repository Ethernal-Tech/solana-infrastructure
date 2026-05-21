package sendtx

import (
	"fmt"
	"sort"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
)

// BridgeTransactionALTAddresses returns the full set of account keys that the
// bridge_transaction instruction references and that are safe to resolve
// through an Address Lookup Table.
//
// The returned slice is intentionally:
//
//   - Ordered: 6 deployment-stable "global" keys followed by 3 keys per
//     registered token ID (mint, token registry PDA, vault ATA). Order is
//     deterministic so callers that persist ALT contents can diff predictably.
//   - Free of sender / receiver data: the fee payer and per-transfer
//     recipient / recipient-ATA keys are intentionally excluded because they
//     vary per transaction.
//   - Free of instruction program IDs: the skyline program and the ed25519
//     program must remain in the static account keys and therefore cannot be
//     moved into an ALT.
//
// Typical usage:
//
//   - Bootstrap: call with the full set of currently-registered token IDs once,
//     then feed the result into ALTAdmin.NewExtendInstructions to populate
//     a fresh ALT.
//   - Token registration: call again with the full (or just the newly-added)
//     token set. ALTAdmin deduplicates against what's already in the ALT, so
//     the emitted extend instruction will carry only the 3 new entries.
func (txSnd *TxSender) BridgeTransactionALTAddresses(
	programID solana.PublicKey,
	mints map[uint16]solana.PublicKey,
) ([]solana.PublicKey, error) {
	if err := requireBridgeProgramID(programID); err != nil {
		return nil, err
	}

	if err := txSnd.instructionConfig.ApplyOptions(
		WithProgramID(programID),
		WithValidatorSetPDA(),
		WithVaultPDA(),
	); err != nil {
		return nil, fmt.Errorf("failed to derive skyline program PDAs: %w", err)
	}

	addresses := make([]solana.PublicKey, 0, 6+len(mints)*3)
	addresses = append(addresses,
		txSnd.instructionConfig.validatorSetPDA,
		txSnd.instructionConfig.vaultPDA,
		txSnd.instructionConfig.tokenProgramID,
		txSnd.instructionConfig.systemProgramID,
		txSnd.instructionConfig.splAssociatedTokenAccountProgramID,
		solana.SysVarInstructionsPubkey,
	)

	tokenIDs := make([]int, 0, len(mints))
	for tokenID := range mints {
		tokenIDs = append(tokenIDs, int(tokenID))
	}

	sort.Ints(tokenIDs)

	for _, rawTokenID := range tokenIDs {
		tokenID := uint16(rawTokenID) //nolint:gosec // values came from uint16 map keys.
		mint := mints[tokenID]

		if mint == skyline_program.NATIVE_SOL_MINT {
			// skip because public key cannot be zero
			continue
		}

		if err := wallet.ValidatePublicKey(mint, true); err != nil {
			return nil, fmt.Errorf("invalid mint %s: %w", mint.String(), err)
		}

		if err := txSnd.instructionConfig.ApplyOptions(WithTokenRegistryPDA(tokenID)); err != nil {
			return nil, fmt.Errorf(
				"failed to derive token registry PDA for mint %s: %w", mint.String(), err)
		}

		vaultATA, _, err := wallet.FindAssociatedTokenAddress(
			txSnd.instructionConfig.vaultPDA, mint)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to find vault associated token account for mint %s: %w",
				mint.String(), err)
		}

		addresses = append(addresses,
			mint,
			txSnd.instructionConfig.tokenRegistryPDA,
			vaultATA,
		)
	}

	return addresses, nil
}
