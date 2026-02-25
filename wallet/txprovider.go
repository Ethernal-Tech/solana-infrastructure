package wallet

import (
	"context"
	"fmt"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
)

type Provider struct {
	rpcClient *rpc.Client
	wsClient  *ws.Client
}

func NewProvider(endpoint string) (*Provider, error) {
	wsCli, err := ws.Connect(context.Background(), rpc.LocalNet_WS)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to localnet: %w", err)
	}

	return &Provider{
		rpcClient: rpc.New(endpoint),
		wsClient:  wsCli,
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

func (p *Provider) ExecuteInstruction(
	ctx context.Context, ix *solana.Instruction,
	feePayer solana.PrivateKey) (*solana.Signature, error) {
	blockHash, err := p.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest blockhash: %w", err)
	}

	tx, err := solana.NewTransactionBuilder().SetRecentBlockHash(blockHash.Value.Blockhash).
		SetFeePayer(feePayer.PublicKey()).AddInstruction(*ix).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build transaction: %w", err)
	}

	signature, err := p.rpcClient.SendTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	if err = p.WaitForSignature(signature, rpc.CommitmentFinalized); err != nil {
		return nil, fmt.Errorf("error while waiting for signature: %w", err)
	}

	return &signature, nil
}

func (p *Provider) GetSignatureStatus(
	ctx context.Context, sig solana.Signature) (*rpc.GetSignatureStatusesResult, error) {
	return p.rpcClient.GetSignatureStatuses(
		ctx,
		true,
		sig,
	)
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

func (p *Provider) WaitForSignature(sig solana.Signature, commitment rpc.CommitmentType) error {
	sub, err := p.wsClient.SignatureSubscribe(sig, commitment)
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	rd := <-sub.Response()
	if rd.Value.Err != nil {
		return fmt.Errorf("transaction failed: %v", rd.Value.Err)
	}

	return nil
}
