package wallet

import "math/big"

type TokenAmount struct {
	TokenMint string   `json:"token_mint"`
	Amount    *big.Int `json:"amount"`
}
