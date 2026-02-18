package wallet

import (
	"context"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

type Provider struct {
	client *rpc.Client
}

func NewProvider(endpoint string) *Provider {
	return &Provider{
		client: rpc.New(endpoint),
	}
}

func (p *Provider) GetBalance(ctx context.Context, pubkey solana.PublicKey) (uint64, error) {
	out, err := p.client.GetBalance(
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

func (p *Provider) GetAccountInfo(ctx context.Context, pubkey solana.PublicKey) (*rpc.GetAccountInfoResult, error) {
	return p.client.GetAccountInfoWithOpts(
		ctx,
		pubkey,
		&rpc.GetAccountInfoOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentConfirmed,
		},
	)
}

func (p *Provider) GetLatestBlockhash(ctx context.Context) (solana.Hash, error) {
	res, err := p.client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return solana.Hash{}, err
	}

	return res.Value.Blockhash, nil
}

func (p *Provider) SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	return p.client.SendTransactionWithOpts(
		ctx,
		tx,
		rpc.TransactionOpts{
			SkipPreflight:       false,
			PreflightCommitment: rpc.CommitmentConfirmed,
		},
	)
}

func (p *Provider) GetSignatureStatus(ctx context.Context, sig solana.Signature) (*rpc.GetSignatureStatusesResult, error) {
	return p.client.GetSignatureStatuses(
		ctx,
		true,
		sig,
	)
}

func (p *Provider) GetSlot(ctx context.Context) (uint64, error) {
	return p.client.GetSlot(ctx, rpc.CommitmentConfirmed)
}

func (p *Provider) GetBlockHeight(ctx context.Context) (uint64, error) {
	return p.client.GetBlockHeight(ctx, rpc.CommitmentFinalized)
}

func (p *Provider) GetBlock(ctx context.Context, slot uint64) (*rpc.GetBlockResult, error) {
	return p.client.GetBlockWithOpts(
		ctx,
		slot,
		&rpc.GetBlockOpts{
			Commitment: rpc.CommitmentConfirmed,
		},
	)
}

func (p *Provider) GetTransaction(
	ctx context.Context,
	sig solana.Signature,
) (*rpc.GetTransactionResult, error) {

	return p.client.GetTransaction(
		ctx,
		sig,
		&rpc.GetTransactionOpts{
			Commitment: rpc.CommitmentConfirmed,
		},
	)
}

func (p *Provider) GetSignaturesForAddress(
	ctx context.Context,
	address solana.PublicKey,
	limit int,
) ([]*rpc.TransactionSignature, error) {

	return p.client.GetSignaturesForAddressWithOpts(
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
	return p.client.SimulateTransactionWithOpts(
		ctx,
		tx,
		&rpc.SimulateTransactionOpts{
			SigVerify: false,
		},
	)
}
