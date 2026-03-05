package wallet

type TokenAmount struct {
	TokenMint string `json:"token_id"`
	Amount    uint64 `json:"val"`
}
