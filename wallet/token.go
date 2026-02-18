package wallet

import "github.com/gagliardetto/solana-go"

type TokenAmount struct {
	TokenMint solana.PublicKey `json:"token_id"`
	Amount    uint64           `json:"val"`
}
