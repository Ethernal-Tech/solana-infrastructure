package sendtx

import (
	"fmt"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx/skyline_program"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
)

type InstructionType string

const (
	InstructionCreateInstruction     InstructionType = "create_instruction"
	InstructionTypeSOLTransfer       InstructionType = "transfer"
	InstructionTypeSPLTransfer       InstructionType = "spl_transfer"
	InstructionTypeBridgingRequest   InstructionType = "bridge_request"
	InstructionTypeBridgeTransaction InstructionType = "bridge_transaction"
	InstructionTypeBridgeVsu         InstructionType = "bridge_vsu"
	InstructionTypeInitialize        InstructionType = "bridge_initialize"
)

type InstructionConfig struct {
	programKey solana.PublicKey

	validatorSetPDA  solana.PublicKey
	vaultPDA         solana.PublicKey
	feeConfigPDA     solana.PublicKey
	tokenRegistryPDA solana.PublicKey

	tokenProgramID                     solana.PublicKey
	systemProgramID                    solana.PublicKey
	splAssociatedTokenAccountProgramID solana.PublicKey
}

type InstructionConfigOption func(c *InstructionConfig) error

func NewInstructionConfig(
	programKey solana.PublicKey, options ...InstructionConfigOption) (*InstructionConfig, error) {
	cfg := &InstructionConfig{
		programKey:                         programKey,
		tokenProgramID:                     solana.TokenProgramID,
		systemProgramID:                    solana.SystemProgramID,
		splAssociatedTokenAccountProgramID: solana.SPLAssociatedTokenAccountProgramID,
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

func WithTokenRegistryPDA() InstructionConfigOption {
	return func(c *InstructionConfig) error {
		tokenRegistryPDA, err := c.derivePDA(skyline_program.TOKEN_REGISTRY_SEED)
		if err != nil {
			return fmt.Errorf("failed to derive token regirstry PDA: %w", err)
		}

		c.tokenRegistryPDA = *tokenRegistryPDA

		return nil
	}
}

func (c *InstructionConfig) derivePDA(seed []byte) (*solana.PublicKey, error) {
	pda, _, err := solana.FindProgramAddress([][]byte{seed}, c.programKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive token regirstry PDA: %w", err)
	}

	if err := wallet.ValidatePublicKey(pda, true); err != nil {
		return nil, fmt.Errorf("tokenRegistryPDA is invalid: %w", err)
	}

	return &pda, nil
}
