package wallet

import (
	"context"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"
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
}

type IChainDataRetriever interface {
	GetLatestBlockhash(ctx context.Context) (solana.Hash, error)
	GetSlot(ctx context.Context) (uint64, error)
	GetBlock(ctx context.Context, slot uint64) (*rpc.GetBlockResult, error)
	GetBlockHeight(ctx context.Context) (uint64, error)
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
	rpcClient *rpc.Client
}

func NewProvider(endpoint string) (*Provider, error) {
	return &Provider{
		rpcClient: rpc.New(endpoint),
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

	// returns in lamports 1 SOL = 1e9 lamports
	return out.Value, nil
}

func (p *Provider) GetTokenAccountBalance(
	ctx context.Context,
	pubkey solana.PublicKey,
) (*rpc.GetTokenAccountBalanceResult, error) {
	return p.rpcClient.GetTokenAccountBalance(
		ctx,
		pubkey,
		rpc.CommitmentConfirmed,
	)
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

func (p *Provider) GetLatestBlockhash(ctx context.Context) (solana.Hash, error) {
	res, err := p.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return solana.Hash{}, err
	}

	return res.Value.Blockhash, nil
}

func (p *Provider) GetSlot(ctx context.Context) (uint64, error) {
	return p.rpcClient.GetSlot(ctx, rpc.CommitmentConfirmed)
}

func (p *Provider) GetBlockHeight(ctx context.Context) (uint64, error) {
	return p.rpcClient.GetBlockHeight(ctx, rpc.CommitmentFinalized)
}

func (p *Provider) GetBlock(ctx context.Context, slot uint64) (*rpc.GetBlockResult, error) {
	return p.rpcClient.GetBlockWithOpts(
		ctx,
		slot,
		&rpc.GetBlockOpts{
			Commitment: rpc.CommitmentConfirmed,
		},
	)
}

func (p *Provider) SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	return p.rpcClient.SendTransactionWithOpts(
		ctx,
		tx,
		rpc.TransactionOpts{
			SkipPreflight:       false,
			PreflightCommitment: rpc.CommitmentConfirmed,
		},
	)
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
			Commitment: rpc.CommitmentConfirmed,
		},
	)
}

func (p *Provider) GetSignaturesForAddress(
	ctx context.Context, address solana.PublicKey, limit int) ([]*rpc.TransactionSignature, error) {
	return p.rpcClient.GetSignaturesForAddressWithOpts(
		ctx,
		address,
		&rpc.GetSignaturesForAddressOpts{
			Limit: &limit,
		},
	)
}

func (p *Provider) SimulateTransaction(
	ctx context.Context,
	tx *solana.Transaction,
) (*rpc.SimulateTransactionResponse, error) {
	return p.rpcClient.SimulateTransactionWithOpts(
		ctx,
		tx,
		&rpc.SimulateTransactionOpts{
			SigVerify: false,
		},
	)
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
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (p *Provider) RequestSolAirdrop(
	ctx context.Context,
	address solana.PublicKey,
	amount uint64,
) (solana.Signature, error) {
	return p.rpcClient.RequestAirdrop(ctx, address, amount, rpc.CommitmentFinalized)
}
