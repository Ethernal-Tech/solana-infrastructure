package sendtx

import (
	"github.com/gagliardetto/solana-go"
)

// CreateTxOption configures how (*TxSender).CreateTx builds a transaction.
type CreateTxOption func(*createTxOptions)

type createTxOptions struct {
	addressTables map[solana.PublicKey]solana.PublicKeySlice
}

// WithAddressLookupTables attaches one or more Address Lookup Tables to the
// transaction. Any account referenced by the instructions that is present in
// one of the tables will be stored as a compact 1-byte index in the serialized
// transaction instead of a full 32-byte public key, freeing up transaction
// size.
//
// Passing a non-empty map also forces the message to be serialized as v0;
// legacy transactions do not support ALTs.
//
// The map key is the ALT account address, and the value is the ordered list
// of addresses stored in that ALT (typically obtained via
// wallet.AddressLookupTableResolver.Resolve).
func WithAddressLookupTables(tables map[solana.PublicKey]solana.PublicKeySlice) CreateTxOption {
	return func(o *createTxOptions) {
		o.addressTables = tables
	}
}

func applyCreateTxOptions(opts []CreateTxOption) *createTxOptions {
	cfg := &createTxOptions{}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(cfg)
	}

	return cfg
}
