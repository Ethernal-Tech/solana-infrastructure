package wallet

import (
	"errors"
	"fmt"

	"github.com/gagliardetto/solana-go"
)

var ErrInvalidSignature = errors.New("invalid signature")

type Wallet struct {
	PublicKey  solana.PublicKey  `json:"pub_key"`
	PrivateKey solana.PrivateKey `json:"priv_key"`
}

func NewWallet() (*Wallet, error) {
	privateKey, err := solana.NewRandomPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	return &Wallet{
		PublicKey:  privateKey.PublicKey(),
		PrivateKey: privateKey,
	}, nil
}

func NewWalletFromPrivateKey(privateKey string) (*Wallet, error) {
	wallet, err := solana.PrivateKeyFromBase58(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create wallet from private key: %w", err)
	}

	return &Wallet{
		PublicKey:  wallet.PublicKey(),
		PrivateKey: wallet,
	}, nil
}

func (w *Wallet) Sign(payload []byte) (*solana.Signature, error) {
	signature, err := w.PrivateKey.Sign(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to sign the payload: %w", err)
	}

	return &signature, nil
}

func (w *Wallet) VerifySignature(payload []byte, signature solana.Signature) error {
	if !w.PrivateKey.PublicKey().Verify(payload, signature) {
		return ErrInvalidSignature
	}

	return nil
}

func (w *Wallet) GetKeys() (solana.PrivateKey, solana.PublicKey) {
	return w.PrivateKey, w.PublicKey
}

func (w *Wallet) ValidatePrivateKey() error {
	return w.PrivateKey.Validate()
}
