package wallet

import "math/big"

type TokenAmount struct {
	TokenMint string   `json:"token_id"`
	Amount    *big.Int `json:"val"`
}
