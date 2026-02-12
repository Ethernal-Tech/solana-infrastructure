package wallet

import (
	"github.com/gagliardetto/solana-go"
)

func GenerateWallet() *solana.Wallet {
	return solana.NewWallet()
}
