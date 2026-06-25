package wallet

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	cardanowallet "github.com/Ethernal-Tech/cardano-infrastructure/wallet"
)

func GetAddressBalanceLamports(ctx context.Context, addr, jsonRPCAddr string) (map[string]*big.Int, error) {
	txProvider, err := NewProvider(jsonRPCAddr, nil)
	if err != nil {
		return nil, fmt.Errorf("new provider: %w", err)
	}

	pubKey, err := PublicKeyFromAddress(addr)
	if err != nil {
		return nil, err
	}

	balance, err := txProvider.GetBalance(ctx, pubKey)
	if err != nil {
		return nil, err
	}

	return map[string]*big.Int{cardanowallet.AdaTokenName: big.NewInt(int64(balance))}, nil
}

func GetAddressBalanceWithTokenNameLamports(
	ctx context.Context, addr, jsonRPCAddr, tokenName string) (map[string]*big.Int, error) {
	pubKey, err := PublicKeyFromAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("GetAddressBalanceWithTokenName parse address: %w", err)
	}

	mintPubKey, err := PublicKeyFromAddress(tokenName)
	if err != nil {
		return nil, fmt.Errorf("GetAddressBalanceWithTokenName parse mint address: %w", err)
	}

	ata, _, err := FindAssociatedTokenAddress(pubKey, mintPubKey)
	if err != nil {
		return nil, fmt.Errorf("GetAddressBalanceWithTokenName find associated token address: %w", err)
	}

	txProvider, err := NewProvider(jsonRPCAddr, nil)
	if err != nil {
		return nil, fmt.Errorf("new provider: %w", err)
	}

	res, err := txProvider.GetTokenAccountBalance(ctx, ata)
	if err != nil {
		// Missing ATA means this wallet does not hold this token yet.
		if strings.Contains(err.Error(), "could not find account") {
			return map[string]*big.Int{tokenName: big.NewInt(0)}, nil
		}

		return map[string]*big.Int{tokenName: big.NewInt(0)}, err // return 0 so caller can still log "failed to query"
	}

	if res == nil || res.Value == nil {
		return map[string]*big.Int{tokenName: big.NewInt(0)}, nil
	}

	amountBigInt, ok := new(big.Int).SetString(res.Value.Amount, 10)
	if !ok {
		return map[string]*big.Int{tokenName: big.NewInt(0)},
			fmt.Errorf("GetAddressBalanceWithTokenName parse amount: %s", res.Value.Amount)
	}

	return map[string]*big.Int{tokenName: amountBigInt}, nil
}
