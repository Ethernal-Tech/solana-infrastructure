package sendtx

import (
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
)

type ChainConfig struct {
	MinAmountToBridge     uint64
	MinFeeForBridging     uint64
	MinOperationFeeAmount uint64
	TreasuryAddress       solana.PublicKey
	BridgingFeeAddress    solana.PublicKey
	CurrencyTokenID       uint16
}

type BridgingTxReceiver struct {
	Address     string             `json:"addr"`
	TokenAmount wallet.TokenAmount `json:"token_amount"`
}

type BridgeRequestDto struct {
	SrcChainID   uint8
	DstChainID   uint8
	SenderAddr   string
	Receivers    []BridgingTxReceiver
	BridgingFee  uint64
	OperationFee uint64
}

type BridgeTransactionDto struct {
	SrcChainID uint8
	DstChainID uint8
	SenderAddr string
	Receivers  []BridgingTxReceiver
	BatchID    uint64
}

type BridgeVSUDto struct {
	SenderAddr             string
	AddingValidatorAddrs   []string
	RemovingValidatorAddrs []string
	BatchID                uint64
}

type InitializeDto struct {
	SenderAddr string
	Validators []string
	LastID     uint64
}

type SOLTransferDto struct {
	SenderPublicKey   string
	ReceiverPublicKey string
	Amount            uint64
}

type SPLTransferDto struct {
	SenderPublicKey   string
	ReceiverPublicKey string
	Amount            uint64
	MintTokenAddress  string
	TokenDecimals     uint8
}

type CreateInstructionDto struct {
	SenderPublicKey   string
	MintTokenAddress  string
	ReceiverPublicKey string
}
