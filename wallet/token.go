package wallet

import (
	"fmt"
	"math/big"

	bin "github.com/gagliardetto/binary"
)

type TokenAmount struct {
	TokenMint string   `json:"token_mint"`
	Amount    *big.Int `json:"amount"`
}

// MarshalWithEncoder provides deterministic serialization for TokenAmount.
// We serialize Amount as a base-10 string to preserve arbitrary precision.
func (t TokenAmount) MarshalWithEncoder(encoder *bin.Encoder) error {
	if err := encoder.Encode(t.TokenMint); err != nil {
		return err
	}

	amount := "0"
	if t.Amount != nil {
		amount = t.Amount.String()
	}

	return encoder.Encode(amount)
}

// UnmarshalWithDecoder restores TokenAmount from the encoded token mint and amount string.
func (t *TokenAmount) UnmarshalWithDecoder(decoder *bin.Decoder) error {
	if err := decoder.Decode(&t.TokenMint); err != nil {
		return err
	}

	var amountStr string
	if err := decoder.Decode(&amountStr); err != nil {
		return err
	}

	amount := new(big.Int)
	if _, ok := amount.SetString(amountStr, 10); !ok {
		return fmt.Errorf("invalid amount: %q", amountStr)
	}

	t.Amount = amount

	return nil
}
