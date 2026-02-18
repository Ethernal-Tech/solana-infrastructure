package wallet

import (
	"fmt"

	"github.com/gagliardetto/solana-go"
)

// ValidateAddress checks if the provided address is a valid Solana wallet address
// AcceptOffCurve indicates whether to accept off-curve public keys (e.g., for PDAs) as valid addresses
func ValidateAddress(address string, acceptOffCurve bool) error {
	pubKey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}

	if !acceptOffCurve && !pubKey.IsOnCurve() {
		return fmt.Errorf("invalid address: public key is off-curve")
	}

	return nil
}
