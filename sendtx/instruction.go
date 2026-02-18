package sendtx

import "github.com/gagliardetto/solana-go"

type InstructionConfig struct {
	bridgingTxDto BridgingTxDto

	bridgingBatchID uint64
	instructionSeed []byte
	validatorSetPDA solana.PublicKey
	bridgingPDA     solana.PublicKey
	vaultPDA        solana.PublicKey

	tokenProgramID                     solana.PublicKey
	systemProgramID                    solana.PublicKey
	SPLAssociatedTokenAccountProgramID solana.PublicKey
}

type InstructionConfigOption func(c *InstructionConfig)

func NewInstructionConfig(bridgingTxDto BridgingTxDto, options ...InstructionConfigOption) *InstructionConfig {
	cfg := &InstructionConfig{
		bridgingTxDto: bridgingTxDto,
	}

	for _, option := range options {
		option(cfg)
	}

	return cfg
}

func WithBridgingBatchID(bridgingBatchID uint64) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.bridgingBatchID = bridgingBatchID
	}
}

func WithInstructionSeed(instructionSeed []byte) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.instructionSeed = instructionSeed
	}
}

func WithValidatorSetPDA(validatorSetPDA solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.validatorSetPDA = validatorSetPDA
	}
}

func WithBridgingPDA(bridgingPDA solana.PublicKey) InstructionConfigOption {
	return func(c *InstructionConfig) {
		c.bridgingPDA = bridgingPDA
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
		c.SPLAssociatedTokenAccountProgramID = splAssociatedTokenAccountProgramID
	}
}
