package sendtx

import (
	"fmt"

	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
)

type InstructionType string

const (
	InstructionTypeBridgingRequest   InstructionType = "bridge_request"
	InstructionTypeBridgeTransaction InstructionType = "bridge_transaction"
	InstructionTypeBridgeVsu         InstructionType = "bridge_vsu"
	InstructionTypeInitialize        InstructionType = "bridge_initialize"
)

type InstructionConfig struct {
	programKeyPair solana.PrivateKey

	validatorSetPDA solana.PublicKey
	vaultPDA        solana.PublicKey

	tokenProgramID                     solana.PublicKey
	systemProgramID                    solana.PublicKey
	splAssociatedTokenAccountProgramID solana.PublicKey
}

type InstructionConfigOption func(c *InstructionConfig)

func NewInstructionConfig(options ...InstructionConfigOption) *InstructionConfig {
	cfg := &InstructionConfig{}

	for _, option := range options {
		option(cfg)
	}

	return cfg
}

func (c *InstructionConfig) Validate() error {
	var errs []error

	// Check required fields
	if !c.programKeyPair.IsValid() {
		errs = append(errs, fmt.Errorf("programKeyPair is required"))
	}

	if err := wallet.ValidatePublicKey(c.validatorSetPDA, true); err != nil {
		errs = append(errs, fmt.Errorf("validatorSetPDA is invalid: %w", err))
	}

	if err := wallet.ValidatePublicKey(c.vaultPDA, true); err != nil {
		errs = append(errs, fmt.Errorf("vaultPDA is invalid: %w", err))
	}

	if c.tokenProgramID != solana.TokenProgramID {
		errs = append(errs, fmt.Errorf("tokenProgramID must be the default Solana Token Program ID"))
	}

	if c.systemProgramID != solana.SystemProgramID {
		errs = append(errs, fmt.Errorf("systemProgramID must be the default Solana System Program ID"))
	}

	if c.splAssociatedTokenAccountProgramID != solana.SPLAssociatedTokenAccountProgramID {
		errs = append(errs,
			fmt.Errorf("SPLAssociatedTokenAccountProgramID must be the default Solana SPL Associated Token Account Program ID"))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed: %w", errs[0])
	}

	return nil
}

func (c *InstructionConfig) GetProgramID() solana.PublicKey {
	return c.programKeyPair.PublicKey()
}

func WithValidatorSetPDA(validatorSetPDA solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.validatorSetPDA = validatorSetPDA
	}
}

func WithVaultPDA(vaultPDA solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.vaultPDA = vaultPDA
	}
}

func WithTokenProgramID(tokenProgramID solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.tokenProgramID = tokenProgramID
	}
}

func WithSystemProgramID(systemProgramID solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.systemProgramID = systemProgramID
	}
}

func WithSPLAssociatedTokenAccountProgramID(
	splAssociatedTokenAccountProgramID solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.splAssociatedTokenAccountProgramID = splAssociatedTokenAccountProgramID
	}
}
