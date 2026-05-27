package sendtx

import (
	"bytes"
	"context"

	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
)

var Ed25519ProgramID = solana.MustPublicKeyFromBase58("Ed25519SigVerify111111111111111111111111111")

type ChainConfig struct {
	MinAmountToBridge     uint64
	MinFeeForBridging     uint64
	MinOperationFeeAmount uint64
	TreasuryAddress       solana.PublicKey
	CurrencyTokenID       uint16
}

type SolanaPayload struct {
	Blockhash [32]byte          `json:"blockhash"`
	Receivers []PayloadReceiver `json:"receivers"`
	FeeAmount uint64            `json:"fee_amount"`
	BatchID   uint64            `json:"batch_id"`
}

func (payload SolanaPayload) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := bin.NewBinEncoder(&buf)

	if err := enc.Encode(payload); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (payload *SolanaPayload) Unmarshal(raw []byte) error {
	dec := bin.NewBinDecoder(raw)

	return dec.Decode(payload)
}

type PayloadReceiver struct {
	Address     [32]byte           `json:"address"`
	TokenAmount wallet.TokenAmount `json:"token_amount"`
}

type BridgingTxReceiver struct {
	Address     string             `json:"address"`
	TokenAmount wallet.TokenAmount `json:"token_amount"`
}

type BridgeRequestDto struct {
	Ctx          context.Context
	ProgramID    solana.PublicKey
	DstChainID   string
	SenderAddr   string
	Receivers    []BridgingTxReceiver
	BridgingFee  uint64
	OperationFee uint64
}

type TransferItem struct {
	Recipient solana.PublicKey
	MintIndex uint8
	Amount    uint64
}

type BridgeTransactionDto struct {
	Ctx            context.Context
	ProgramID      solana.PublicKey
	SenderAddr     string
	Receivers      []PayloadReceiver
	PayloadBytes   []byte
	SignaturePairs map[solana.PublicKey]solana.Signature
}

type BridgeVSUDto struct {
	ProgramID              solana.PublicKey
	SenderAddr             string
	AddingValidatorAddrs   []string
	RemovingValidatorAddrs []string
	BatchID                uint64
}

type HotWalletIncrementDto struct {
	ProgramID  solana.PublicKey
	SenderAddr string
	TokenMint  string
	TokenID    uint16
	Amount     uint64
}

type InitializeDto struct {
	ProgramID     solana.PublicKey
	AuthorityAddr string
	Validators    []string
	LastID        uint64
}

type RegisterTokenLockUnlockDto struct {
	ProgramID         solana.PublicKey
	AuthorityAddr     string
	TokenMint         string
	TokenID           uint16
	MinBridgingAmount uint64
}

type RegisterTokenMintBurnDto struct {
	ProgramID         solana.PublicKey
	AuthorityAddr     string
	TokenMint         string
	TokenID           uint16
	MinBridgingAmount uint64
	Decimals          uint8
	Name              string
	Symbol            string
	URI               string
}

type UpdateFeeConfigDto struct {
	ProgramID       solana.PublicKey
	AuthorityAddr   string
	MinOperationFee uint64
	BridgingFee     uint64

	UpdateTreasury     bool
	NewTreasuryAddress string
}

type UpdateProgramVersionDto struct {
	ProgramID     solana.PublicKey
	AuthorityAddr string
	VersionString string
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
