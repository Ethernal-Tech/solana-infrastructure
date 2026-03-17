package wallet

import (
	"bytes"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
)

func MarshalTransaction(tx *solana.Transaction) ([]byte, error) {
	var buf bytes.Buffer

	enc := bin.NewBinEncoder(&buf)
	if err := tx.MarshalWithEncoder(enc); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func UnmarshalTransaction(raw []byte) (*solana.Transaction, error) {
	dec := bin.NewBinDecoder(raw)

	var tx solana.Transaction
	if err := tx.UnmarshalWithDecoder(dec); err != nil {
		return nil, err
	}

	return &tx, nil
}
