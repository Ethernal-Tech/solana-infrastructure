package sendtx

import (
	"encoding/binary"
	"fmt"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
)

type InstructionType string

const (
	InstructionCreateInstruction      InstructionType = "create_instruction"
	InstructionTypeSOLTransfer        InstructionType = "transfer"
	InstructionTypeSPLTransfer        InstructionType = "spl_transfer"
	InstructionTypeBridgingRequest    InstructionType = "bridge_request"
	InstructionTypeBridgeTransaction  InstructionType = "bridge_transaction"
	InstructionTypeBridgeVsu          InstructionType = "bridge_vsu"
	InstructionTypeHotWalletIncrement InstructionType = "hot_wallet_increment"
	InstructionTypeInitialize         InstructionType = "bridge_initialize"

	InstructionTypeRegisterTokensLockUnlock InstructionType = "register_tokens_lock_unlock"
	InstructionTypeRegisterTokensMintBurn   InstructionType = "register_tokens_mint_burn"
	InstructionTypeUpdateFeeConfig          InstructionType = "update_fee_config"
)

type InstructionConfig struct {
	programKey solana.PublicKey

	validatorSetPDA  solana.PublicKey
	vaultPDA         solana.PublicKey
	feeConfigPDA     solana.PublicKey
	tokenRegistryPDA solana.PublicKey
	tokenIDGuardPDA  solana.PublicKey
	metadataPDA      solana.PublicKey

	tokenProgramID                     solana.PublicKey
	tokenMetadataProgramID             solana.PublicKey
	systemProgramID                    solana.PublicKey
	splAssociatedTokenAccountProgramID solana.PublicKey
	rentPubkey                         solana.PublicKey
}

type InstructionConfigOption func(c *InstructionConfig) error

func NewInstructionConfig(
	options ...InstructionConfigOption) (*InstructionConfig, error) {
	cfg := &InstructionConfig{
		programKey:                         skyline_program.ProgramID,
		tokenProgramID:                     solana.TokenProgramID,
		tokenMetadataProgramID:             solana.TokenMetadataProgramID,
		systemProgramID:                    solana.SystemProgramID,
		splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
		rentPubkey:                         solana.SysVarRentPubkey,
	}

	if err := cfg.ApplyOptions(options...); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *InstructionConfig) ApplyOptions(options ...InstructionConfigOption) error {
	for _, option := range options {
		if err := option(c); err != nil {
			return err
		}
	}

	return nil
}

func WithValidatorSetPDA() InstructionConfigOption {
	return func(c *InstructionConfig) error {
		validatorSetPDA, err := c.derivePDA(skyline_program.VALIDATOR_SET_SEED)
		if err != nil {
			return fmt.Errorf("failed to derive validator set PDA: %w", err)
		}

		c.validatorSetPDA = *validatorSetPDA

		return nil
	}
}

func WithVaultPDA() InstructionConfigOption {
	return func(c *InstructionConfig) error {
		vaultPDA, err := c.derivePDA(skyline_program.VAULT_SEED)
		if err != nil {
			return fmt.Errorf("failed to derive vault PDA: %w", err)
		}

		c.vaultPDA = *vaultPDA

		return nil
	}
}

func WithFeeConfigPDA() InstructionConfigOption {
	return func(c *InstructionConfig) error {
		feeConfigPDA, err := c.derivePDA(skyline_program.FEE_CONFIG_SEED)
		if err != nil {
			return fmt.Errorf("failed to derive fee config PDA: %w", err)
		}

		c.feeConfigPDA = *feeConfigPDA

		return nil
	}
}

func WithTokenRegistryPDA(mintAccount solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) error {
		tokenRegistryPDA, err := c.derivePDA(
			skyline_program.TOKEN_REGISTRY_SEED,
			mintAccount[:],
		)
		if err != nil {
			return fmt.Errorf("failed to derive token regirstry PDA: %w", err)
		}

		c.tokenRegistryPDA = *tokenRegistryPDA

		return nil
	}
}

func WithMetadataPDA(mintAccount solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) error {
		metadataPDA, _, err := solana.FindProgramAddress(
			[][]byte{
				[]byte("metadata"),
				c.tokenMetadataProgramID[:],
				mintAccount[:],
			},
			c.tokenMetadataProgramID,
		)
		if err != nil {
			return fmt.Errorf("failed to derive metadata PDA: %w", err)
		}

		if err := wallet.ValidatePublicKey(metadataPDA, true); err != nil {
			return fmt.Errorf("metadata PDA is invalid: %w", err)
		}

		c.metadataPDA = metadataPDA

		return nil
	}
}

func WithTokenIDGuardPDA(tokenID uint16) InstructionConfigOption {
	return func(c *InstructionConfig) error {
		tokenIDBytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(tokenIDBytes, tokenID)

		tokenIDGuardPDA, err := c.derivePDA(
			skyline_program.TOKEN_ID_GUARD_SEED,
			tokenIDBytes,
		)
		if err != nil {
			return fmt.Errorf("failed to derive token ID guard PDA: %w", err)
		}

		c.tokenIDGuardPDA = *tokenIDGuardPDA

		return nil
	}
}
func (c *InstructionConfig) derivePDA(seeds ...[]byte) (*solana.PublicKey, error) {
	pda, _, err := solana.FindProgramAddress(seeds, c.programKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive token regirstry PDA: %w", err)
	}

	if err := wallet.ValidatePublicKey(pda, true); err != nil {
		return nil, fmt.Errorf("tokenRegistryPDA is invalid: %w", err)
	}

	return &pda, nil
}
