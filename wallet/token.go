package wallet

import (
	bin "github.com/gagliardetto/binary"
)

type TokenAmount struct {
	TokenID uint16 `json:"token_id"`
	Amount  uint64 `json:"amount"`
}

// MarshalWithEncoder provides deterministic serialization for TokenAmount.
// We serialize Amount as a base-10 string to preserve arbitrary precision.
func (t TokenAmount) MarshalWithEncoder(encoder *bin.Encoder) error {
	if err := encoder.Encode(t.TokenID); err != nil {
		return err
	}

	return encoder.Encode(t.Amount)
}

// UnmarshalWithDecoder restores TokenAmount from the encoded token mint and amount string.
func (t *TokenAmount) UnmarshalWithDecoder(decoder *bin.Decoder) error {
	if err := decoder.Decode(&t.TokenID); err != nil {
		return err
	}

	if err := decoder.Decode(&t.Amount); err != nil {
		return err
	}

	return nil
}
