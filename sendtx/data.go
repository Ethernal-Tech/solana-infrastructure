package sendtx

import (
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
)

type ChainConfig struct {
	MultiSigAddress       string
	MinAmountToBridge     uint64
	MinFeeForBridging     uint64
	MinOperationFeeAmount uint64
	Tokens                map[uint16]wallet.TokenAmount
}

type BridgingTxReceiver struct {
	Addr        string             `json:"addr"`
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
