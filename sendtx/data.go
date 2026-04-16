package sendtx

import (
	"bytes"

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
	BridgingFeeAddress    solana.PublicKey
	CurrencyTokenID       uint16
}

type SolanaPayload struct {
	Blockhash string               `json:"blockhash"`
	Receivers []BridgingTxReceiver `json:"receivers"`
	BatchID   uint64               `json:"batch_id"`
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
	SenderAddr     string
	Receivers      []BridgingTxReceiver
	BatchID        uint64
	PayloadBytes   []byte
	SignaturePairs map[solana.PublicKey]solana.Signature
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

type RegisterTokenMintBurnDto struct {
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
