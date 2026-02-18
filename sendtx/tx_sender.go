package sendtx

import (
	"context"
	"fmt"

	infracommon "github.com/Ethernal-Tech/cardano-infrastructure/common"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
)

type TxSender struct {
	minAmountToBridge uint64
	chainConfig       ChainConfig
	retryOptions      []infracommon.RetryConfigOption
	instructionConfig InstructionConfig
}

type TxSenderOption func(*TxSender)

func NewTxSender(chainConfig ChainConfig,
	options ...TxSenderOption,
) *TxSender {
	txSnd := &TxSender{
		chainConfig: chainConfig,
	}

	txSnd.minAmountToBridge = max(txSnd.minAmountToBridge, chainConfig.MinAmountToBridge)

	for _, option := range options {
		option(txSnd)
	}

	return txSnd
}

func (txSnd *TxSender) CreateBridgingTx(
	ctx context.Context,
	txDto BridgingTxDto,
) (string, error) {
	err := txSnd.prepareBridgingTx(ctx, txDto)
	if err != nil {
		return "", fmt.Errorf("failed to prepare bridging transaction: %w", err)
	}

	return "", nil
}

func (txSnd *TxSender) prepareBridgingTx(
	ctx context.Context,
	txDto BridgingTxDto,
) error {
	err := wallet.ValidateAddress(txDto.SenderAddr, false)
	if err != nil {
		return err
	}

	if err := checkFees(&txSnd.chainConfig, txDto.BridgingFee, txDto.OperationFee); err != nil {
		return err
	}

	_ = ctx

	return nil
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

func WithMinAmountToBridge(minAmountToBridge uint64) TxSenderOption {
	return func(txSnd *TxSender) {
		txSnd.minAmountToBridge = minAmountToBridge
	}
}

func WithRetryOptions(retryOptions ...infracommon.RetryConfigOption) TxSenderOption {
	return func(txSnd *TxSender) {
		txSnd.retryOptions = retryOptions
	}
}

func WithInstructionConfig(instructionConfig InstructionConfig) TxSenderOption {
	return func(txSnd *TxSender) {
		txSnd.instructionConfig = instructionConfig
	}
}
