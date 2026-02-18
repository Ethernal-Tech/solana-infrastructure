package sendtx

import (
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
)

type ChainConfig struct {
	SolanaCliBinary       string
	MinAmountToBridge     uint64
	MinFeeForBridging     uint64
	MinOperationFeeAmount uint64
	Tokens                map[uint16]wallet.TokenAmount
}

type BridgingTxReceiver struct {
	Addr      string `json:"addr"`
	Amount    uint64 `json:"amount"`
	TokenMint string `json:"token_id"`
}

type BridgingTxDto struct {
	SrcChainID      uint8
	DstChainID      uint8
	SenderAddr      string
	Receivers       []BridgingTxReceiver
	BridgingAddress string
	BridgingFee     uint64
	OperationFee    uint64
}
