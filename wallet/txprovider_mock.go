package wallet

import (
	"context"
	"time"

	"github.com/gagliardetto/solana-go"
	alt "github.com/gagliardetto/solana-go/programs/address-lookup-table"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/mock"
)

// MockTxProvider mocks ITxProvider (IUserDataRetriever + IChainDataRetriever + ITxSubmiter + ITxRetriever).
type MockTxProvider struct {
	mock.Mock
}

var _ ITxProvider = (*MockTxProvider)(nil)

// ─── IUserDataRetriever ───────────────────────────────────────────────────────

func (m *MockTxProvider) GetBalance(ctx context.Context, pubKey solana.PublicKey) (uint64, error) {
	args := m.Called(ctx, pubKey)

	return args.Get(0).(uint64), args.Error(1) //nolint:forcetypeassert
}

func (m *MockTxProvider) GetTokenAccountBalance(
	ctx context.Context,
	pubkey solana.PublicKey,
) (*rpc.GetTokenAccountBalanceResult, error) {
	args := m.Called(ctx, pubkey)
	result, _ := args.Get(0).(*rpc.GetTokenAccountBalanceResult)

	return result, args.Error(1)
}

func (m *MockTxProvider) GetAccountInfo(
	ctx context.Context,
	pubkey solana.PublicKey,
) (*rpc.GetAccountInfoResult, error) {
	args := m.Called(ctx, pubkey)
	result, _ := args.Get(0).(*rpc.GetAccountInfoResult)

	return result, args.Error(1)
}

// ─── IChainDataRetriever ──────────────────────────────────────────────────────

func (m *MockTxProvider) GetLatestBlockhash(ctx context.Context) (solana.Hash, error) {
	args := m.Called(ctx)

	return args.Get(0).(solana.Hash), args.Error(1) //nolint:forcetypeassert
}

func (m *MockTxProvider) GetSlot(ctx context.Context) (uint64, error) {
	args := m.Called(ctx)

	return args.Get(0).(uint64), args.Error(1) //nolint:forcetypeassert
}

func (m *MockTxProvider) GetBlock(ctx context.Context, slot uint64) (*rpc.GetBlockResult, error) {
	args := m.Called(ctx, slot)
	result, _ := args.Get(0).(*rpc.GetBlockResult)

	return result, args.Error(1)
}

func (m *MockTxProvider) GetBlockHeight(ctx context.Context) (uint64, error) {
	args := m.Called(ctx)

	return args.Get(0).(uint64), args.Error(1) //nolint:forcetypeassert
}

// ─── ITxSubmiter ─────────────────────────────────────────────────────────────

func (m *MockTxProvider) SendTransaction(
	ctx context.Context,
	tx *solana.Transaction,
) (solana.Signature, error) {
	args := m.Called(ctx, tx)

	return args.Get(0).(solana.Signature), args.Error(1) //nolint:forcetypeassert
}

func (m *MockTxProvider) WaitForSignature(
	ctx context.Context,
	sig solana.Signature,
	commitment rpc.CommitmentType,
	maxWaitTime time.Duration,
) error {
	args := m.Called(ctx, sig, commitment, maxWaitTime)

	return args.Error(0)
}

// ─── ITxRetriever ─────────────────────────────────────────────────────────────

func (m *MockTxProvider) GetSignatureStatus(
	ctx context.Context,
	sig solana.Signature,
) (*rpc.GetSignatureStatusesResult, error) {
	args := m.Called(ctx, sig)
	result, _ := args.Get(0).(*rpc.GetSignatureStatusesResult)

	return result, args.Error(1)
}

func (m *MockTxProvider) GetSignaturesForAddress(
	ctx context.Context,
	address solana.PublicKey,
	limit int,
) ([]*rpc.TransactionSignature, error) {
	args := m.Called(ctx, address, limit)
	result, _ := args.Get(0).([]*rpc.TransactionSignature)

	return result, args.Error(1)
}

func (m *MockTxProvider) GetTransaction(
	ctx context.Context,
	sig solana.Signature,
) (*rpc.GetTransactionResult, error) {
	args := m.Called(ctx, sig)
	result, _ := args.Get(0).(*rpc.GetTransactionResult)

	return result, args.Error(1)
}

// ─── MockUserDataRetriever ────────────────────────────────────────────────────

type MockUserDataRetriever struct {
	mock.Mock
}

var _ IUserDataRetriever = (*MockUserDataRetriever)(nil)

func (m *MockUserDataRetriever) GetBalance(ctx context.Context, pubKey solana.PublicKey) (uint64, error) {
	args := m.Called(ctx, pubKey)

	return args.Get(0).(uint64), args.Error(1) //nolint:forcetypeassert
}

func (m *MockUserDataRetriever) GetTokenAccountBalance(
	ctx context.Context,
	pubkey solana.PublicKey,
) (*rpc.GetTokenAccountBalanceResult, error) {
	args := m.Called(ctx, pubkey)
	result, _ := args.Get(0).(*rpc.GetTokenAccountBalanceResult)

	return result, args.Error(1)
}

func (m *MockUserDataRetriever) GetAccountInfo(
	ctx context.Context,
	pubkey solana.PublicKey,
) (*rpc.GetAccountInfoResult, error) {
	args := m.Called(ctx, pubkey)
	result, _ := args.Get(0).(*rpc.GetAccountInfoResult)

	return result, args.Error(1)
}

// ─── MockChainDataRetriever ───────────────────────────────────────────────────

type MockChainDataRetriever struct {
	mock.Mock
}

var _ IChainDataRetriever = (*MockChainDataRetriever)(nil)

func (m *MockChainDataRetriever) GetLatestBlockhash(ctx context.Context) (solana.Hash, error) {
	args := m.Called(ctx)

	return args.Get(0).(solana.Hash), args.Error(1) //nolint:forcetypeassert
}

func (m *MockChainDataRetriever) GetSlot(ctx context.Context) (uint64, error) {
	args := m.Called(ctx)

	return args.Get(0).(uint64), args.Error(1) //nolint:forcetypeassert
}

func (m *MockChainDataRetriever) GetBlock(ctx context.Context, slot uint64) (*rpc.GetBlockResult, error) {
	args := m.Called(ctx, slot)
	result, _ := args.Get(0).(*rpc.GetBlockResult)

	return result, args.Error(1)
}

func (m *MockChainDataRetriever) GetBlockHeight(ctx context.Context) (uint64, error) {
	args := m.Called(ctx)

	return args.Get(0).(uint64), args.Error(1) //nolint:forcetypeassert
}

// ─── MockAddressLookupTableFetcher ────────────────────────────────────────────

type MockAddressLookupTableFetcher struct {
	mock.Mock
}

var _ IAddressLookupTableFetcher = (*MockAddressLookupTableFetcher)(nil)

func (m *MockAddressLookupTableFetcher) GetAddressLookupTable(
	ctx context.Context,
	address solana.PublicKey,
) (*alt.AddressLookupTableState, error) {
	args := m.Called(ctx, address)
	result, _ := args.Get(0).(*alt.AddressLookupTableState)

	return result, args.Error(1)
}

// ─── MockTxSubmiter ───────────────────────────────────────────────────────────

type MockTxSubmiter struct {
	mock.Mock
}

var _ ITxSubmiter = (*MockTxSubmiter)(nil)

func (m *MockTxSubmiter) SendTransaction(
	ctx context.Context,
	tx *solana.Transaction,
) (solana.Signature, error) {
	args := m.Called(ctx, tx)

	return args.Get(0).(solana.Signature), args.Error(1) //nolint:forcetypeassert
}

func (m *MockTxSubmiter) WaitForSignature(
	ctx context.Context,
	sig solana.Signature,
	commitment rpc.CommitmentType,
	maxWaitTime time.Duration,
) error {
	args := m.Called(ctx, sig, commitment, maxWaitTime)

	return args.Error(0)
}

// ─── MockTxRetriever ──────────────────────────────────────────────────────────

type MockTxRetriever struct {
	mock.Mock
}

var _ ITxRetriever = (*MockTxRetriever)(nil)

func (m *MockTxRetriever) GetSignatureStatus(
	ctx context.Context,
	sig solana.Signature,
) (*rpc.GetSignatureStatusesResult, error) {
	args := m.Called(ctx, sig)
	result, _ := args.Get(0).(*rpc.GetSignatureStatusesResult)

	return result, args.Error(1)
}

func (m *MockTxRetriever) GetSignaturesForAddress(
	ctx context.Context,
	address solana.PublicKey,
	limit int,
) ([]*rpc.TransactionSignature, error) {
	args := m.Called(ctx, address, limit)
	result, _ := args.Get(0).([]*rpc.TransactionSignature)

	return result, args.Error(1)
}

func (m *MockTxRetriever) GetTransaction(
	ctx context.Context,
	sig solana.Signature,
) (*rpc.GetTransactionResult, error) {
	args := m.Called(ctx, sig)
	result, _ := args.Get(0).(*rpc.GetTransactionResult)

	return result, args.Error(1)
}
