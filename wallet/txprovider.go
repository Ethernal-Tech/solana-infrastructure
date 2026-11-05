package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Ethernal-Tech/solana-infrastructure/common"
	"github.com/gagliardetto/solana-go"
	alt "github.com/gagliardetto/solana-go/programs/address-lookup-table"
	"github.com/gagliardetto/solana-go/rpc"
)

type ITxProvider interface {
	IUserDataRetriever
	IChainDataRetriever
	ITxSubmiter
	ITxRetriever
}

type IUserDataRetriever interface {
	GetBalance(ctx context.Context, pubKey solana.PublicKey) (uint64, error)
	GetTokenAccountBalance(ctx context.Context, pubkey solana.PublicKey) (*rpc.GetTokenAccountBalanceResult, error)
	GetAccountInfo(ctx context.Context, pubkey solana.PublicKey) (*rpc.GetAccountInfoResult, error)
	GetProgramAccounts(
		ctx context.Context,
		programID solana.PublicKey,
		opts *rpc.GetProgramAccountsOpts,
	) (rpc.GetProgramAccountsResult, error)
}

type LatestBlockhashAndExpiry struct {
	Blockhash            solana.Hash
	LastValidBlockHeight uint64
}

type IChainDataRetriever interface {
	GetLatestBlockhash(ctx context.Context) (solana.Hash, error)
	GetLatestBlockhashAndExpiry(ctx context.Context) (LatestBlockhashAndExpiry, error)
	GetSlot(ctx context.Context) (uint64, error)
	GetBlock(ctx context.Context, slot uint64) (*rpc.GetBlockResult, error)
	GetBlockHeight(ctx context.Context) (uint64, error)
}

type IAddressLookupTableFetcher interface {
	GetAddressLookupTable(
		ctx context.Context, address solana.PublicKey) (*alt.AddressLookupTableState, error)
}

type ITxSubmiter interface {
	SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error)
	WaitForSignature(
		ctx context.Context, sig solana.Signature, commitment rpc.CommitmentType, maxWaitTime time.Duration) error
}

type ITxRetriever interface {
	GetSignatureStatus(ctx context.Context, sig solana.Signature) (*rpc.GetSignatureStatusesResult, error)
	GetSignaturesForAddress(ctx context.Context, address solana.PublicKey, limit int) ([]*rpc.TransactionSignature, error)
	GetTransaction(ctx context.Context, sig solana.Signature) (*rpc.GetTransactionResult, error)
}

type Provider struct {
	rpcClient *common.MutexRPCClient
}

func NewProvider(endpoint string, config *common.RPCMethodLimitsConfig) (*Provider, error) {
	globalRPSLimit := 7

	if config != nil {
		if config.GlobalRPSLimit == 0 {
			return nil, fmt.Errorf("global RPS limit cannot be 0")
		}

		globalRPSLimit = config.GlobalRPSLimit
	}

	return &Provider{
		rpcClient: common.NewMutexRPCClient(
			common.NewRateLimitedRPCClient(endpoint, globalRPSLimit, nil),
			config,
		),
	}, nil
}

func (p *Provider) GetBalance(ctx context.Context, pubkey solana.PublicKey) (uint64, error) {
	out, err := p.rpcClient.GetBalance(
		ctx,
		pubkey,
		rpc.CommitmentConfirmed,
	)
	if err != nil {
		return 0, err
	}

	if out == nil {
		return 0, errors.New("getBalance returned empty result")
	}

	// returns in lamports 1 SOL = 1e9 lamports
	return out.Value, nil
}

func (p *Provider) GetTokenAccountBalance(
	ctx context.Context,
	pubkey solana.PublicKey,
) (*rpc.GetTokenAccountBalanceResult, error) {
	out, err := p.rpcClient.GetTokenAccountBalance(
		ctx,
		pubkey,
		rpc.CommitmentConfirmed,
	)
	if err != nil {
		return nil, err
	}

	if out == nil || out.Value == nil {
		return nil, errors.New("getTokenAccountBalance returned empty result")
	}

	return out, nil
}

func (p *Provider) GetAccountInfo(ctx context.Context, pubkey solana.PublicKey) (*rpc.GetAccountInfoResult, error) {
	return p.rpcClient.GetAccountInfoWithOpts(
		ctx,
		pubkey,
		&rpc.GetAccountInfoOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentConfirmed,
		},
	)
}

func (p *Provider) GetProgramAccounts(
	ctx context.Context,
	programID solana.PublicKey,
	opts *rpc.GetProgramAccountsOpts,
) (rpc.GetProgramAccountsResult, error) {
	accounts, err := p.rpcClient.GetProgramAccountsWithOpts(ctx, programID, opts)
	if err != nil {
		return nil, err
	}

	for i, account := range accounts {
		if account == nil || account.Account == nil || account.Account.Data == nil {
			return nil, fmt.Errorf("getProgramAccounts returned empty entry at index %d", i)
		}
	}

	return accounts, nil
}

func (p *Provider) GetLatestBlockhash(ctx context.Context) (solana.Hash, error) {
	res, err := p.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return solana.Hash{}, err
	}

	if res == nil || res.Value == nil {
		return solana.Hash{}, errors.New("getLatestBlockhash returned empty result")
	}

	return res.Value.Blockhash, nil
}

func (p *Provider) GetLatestBlockhashAndExpiry(ctx context.Context) (LatestBlockhashAndExpiry, error) {
	res, err := p.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return LatestBlockhashAndExpiry{}, err
	}

	if res == nil || res.Value == nil {
		return LatestBlockhashAndExpiry{}, errors.New("getLatestBlockhash returned empty result")
	}

	return LatestBlockhashAndExpiry{
		Blockhash:            res.Value.Blockhash,
		LastValidBlockHeight: res.Value.LastValidBlockHeight,
	}, nil
}

func (p *Provider) GetSlot(ctx context.Context) (uint64, error) {
	return p.rpcClient.GetSlot(ctx, rpc.CommitmentConfirmed)
}

func (p *Provider) GetFinalizedSlot(ctx context.Context) (uint64, error) {
	return p.rpcClient.GetSlot(ctx, rpc.CommitmentFinalized)
}

func (p *Provider) GetBlockHeight(ctx context.Context) (uint64, error) {
	return p.rpcClient.GetBlockHeight(ctx, rpc.CommitmentFinalized)
}

func (p *Provider) GetBlock(ctx context.Context, slot uint64) (*rpc.GetBlockResult, error) {
	return p.rpcClient.GetBlockWithOpts(
		ctx,
		slot,
		&rpc.GetBlockOpts{
			Commitment:                     rpc.CommitmentConfirmed,
			MaxSupportedTransactionVersion: common.MaxSupportedTransactionVersion(),
		},
	)
}

func (p *Provider) SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	sig, err := p.rpcClient.SendTransactionWithOpts(
		ctx,
		tx,
		rpc.TransactionOpts{
			SkipPreflight:       false,
			PreflightCommitment: rpc.CommitmentConfirmed,
		},
	)
	if err != nil {
		return solana.Signature{}, err
	}

	if sig.IsZero() {
		return solana.Signature{}, errors.New("sendTransaction returned empty signature")
	}

	return sig, nil
}

func (p *Provider) GetSignatureStatus(
	ctx context.Context,
	sig solana.Signature,
) (*rpc.GetSignatureStatusesResult, error) {
	return p.rpcClient.GetSignatureStatuses(
		ctx,
		true,
		sig,
	)
}

func (p *Provider) GetTransaction(
	ctx context.Context, sig solana.Signature) (*rpc.GetTransactionResult, error) {
	return p.rpcClient.GetTransaction(
		ctx,
		sig,
		&rpc.GetTransactionOpts{
			Commitment:                     rpc.CommitmentConfirmed,
			MaxSupportedTransactionVersion: common.MaxSupportedTransactionVersion(),
		},
	)
}

func (p *Provider) GetSignaturesForAddress(
	ctx context.Context, address solana.PublicKey, limit int) ([]*rpc.TransactionSignature, error) {
	signatures, err := p.rpcClient.GetSignaturesForAddressWithOpts(
		ctx,
		address,
		&rpc.GetSignaturesForAddressOpts{
			Limit: &limit,
		},
	)
	if err != nil {
		return nil, err
	}

	for i, signature := range signatures {
		if signature == nil {
			return nil, fmt.Errorf("getSignaturesForAddress returned empty entry at index %d", i)
		}
	}

	return signatures, nil
}

func (p *Provider) GetAddressLookupTable(
	ctx context.Context, address solana.PublicKey,
) (*alt.AddressLookupTableState, error) {
	account, err := p.rpcClient.GetAccountInfoWithOpts(ctx, address, &rpc.GetAccountInfoOpts{
		Encoding: solana.EncodingBase64,
	})
	if err != nil {
		return nil, err
	}

	if account == nil {
		return nil, fmt.Errorf("account not found")
	}

	return alt.DecodeAddressLookupTableState(account.GetBinary())
}

func (p *Provider) SimulateTransaction(
	ctx context.Context,
	tx *solana.Transaction,
) (*rpc.SimulateTransactionResponse, error) {
	out, err := p.rpcClient.SimulateTransactionWithOpts(
		ctx,
		tx,
		&rpc.SimulateTransactionOpts{
			SigVerify: false,
		},
	)
	if err != nil {
		return nil, err
	}

	if out == nil || out.Value == nil {
		return nil, errors.New("simulateTransaction returned empty result")
	}

	return out, nil
}

func (p *Provider) WaitForSignature(
	ctx context.Context, sig solana.Signature, commitment rpc.CommitmentType, maxWaitTime time.Duration) error {
	for {
		statuses, err := p.rpcClient.GetSignatureStatuses(ctx, true, sig)
		if err != nil {
			return err
		}

		if len(statuses.Value) > 0 && statuses.Value[0] != nil {
			status := statuses.Value[0]
			if status.Err != nil {
				return fmt.Errorf("transaction with signature %s failed: %v", sig.String(), status.Err)
			}

			if status.ConfirmationStatus != "" &&
				// Check if the confirmation status is already finalized, or matches the expected commitment level
				// If the transaction is finalized, there's no need to poll anymore, preventing an infinite loop.
				rpc.CommitmentType(status.ConfirmationStatus) == rpc.CommitmentFinalized ||
				rpc.CommitmentType(status.ConfirmationStatus) == commitment {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(maxWaitTime):
			return fmt.Errorf("timeout while waiting for transaction: %s", sig.String())
		case <-time.After(time.Second):
		}
	}
}

func (p *Provider) RequestSolAirdrop(
	ctx context.Context,
	address solana.PublicKey,
	amount uint64,
) (solana.Signature, error) {
	sig, err := p.rpcClient.RequestAirdrop(ctx, address, amount, rpc.CommitmentFinalized)
	if err != nil {
		return solana.Signature{}, err
	}

	if sig.IsZero() {
		return solana.Signature{}, errors.New("requestAirdrop returned empty signature")
	}

	return sig, nil
}
