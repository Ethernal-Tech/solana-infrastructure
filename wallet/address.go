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

func ValidatePublicKey(pubKey solana.PublicKey, acceptOffCurve bool) error {
	if pubKey.IsZero() {
		return fmt.Errorf("public key cannot be zero")
	}

	if len(pubKey.Bytes()) != solana.PublicKeyLength {
		return fmt.Errorf("invalid length, expected %v, got %d", solana.PublicKeyLength, len(pubKey.Bytes()))
	}

	if _, err := solana.PublicKeyFromBase58(pubKey.String()); err != nil {
		return fmt.Errorf("invalid public key base58 encoding: %w", err)
	}

	if !acceptOffCurve && !pubKey.IsOnCurve() {
		return fmt.Errorf("invalid public key: public key is off-curve")
	}

	return nil
}

func PublicKeyFromAddress(address string) (solana.PublicKey, error) {
	pubKey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("invalid address: %w", err)
	}

	return pubKey, nil
}

func FindAssociatedTokenAddress(walletAddress, tokenMint solana.PublicKey) (solana.PublicKey, uint8, error) {
	associatedTokenAddress, bumpSeed, err := solana.FindAssociatedTokenAddress(walletAddress, tokenMint)
	if err != nil {
		return solana.PublicKey{}, 0, fmt.Errorf("failed to find associated token address: %w", err)
	}

	err = ValidatePublicKey(associatedTokenAddress, true)
	if err != nil {
		return solana.PublicKey{}, 0, fmt.Errorf("invalid associated token address: %w", err)
	}

	return associatedTokenAddress, bumpSeed, nil
}
