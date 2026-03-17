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
	Address     string             `json:"address"`
	TokenAmount wallet.TokenAmount `json:"token_amount"`
}

type BridgeRequestDto struct {
	DstChainID   string
	SenderAddr   string
	Receivers    []BridgingTxReceiver
	BridgingFee  uint64
	OperationFee uint64
}

type BridgeTransactionDto struct {
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
	AuthorityAddr string
	Validators    []string
	LastID        uint64
}

type RegisterTokenLockUnlockDto struct {
	AuthorityAddr     string
	TokenMint         string
	TokenID           uint16
	MinBridgingAmount uint64
}

type UpdateFeeConfigDto struct {
	AuthorityAddr   string
	MinOperationFee uint64
	BridgingFee     uint64

	UpdateTreasury     bool
	UpdateRelayer      bool
	NewTreasuryAddress string
	NewRelayerAddress  string
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
