package store

import (
	"github.com/gagliardetto/solana-go"
	"github.com/test-go/testify/mock"
)

type MockStorageHandler struct {
	mock.Mock
}

var _ StorageHandler = (*MockStorageHandler)(nil)

func (m *MockStorageHandler) Close() error {
	args := m.Called()

	return args.Error(0)
}

func (m *MockStorageHandler) StoreBlock(tx StorageTransaction, bp BlockPoint) error {
	args := m.Called(tx, bp)

	return args.Error(0)
}

func (m *MockStorageHandler) StoreEvent(
	tx StorageTransaction, slot uint64, blockNumber uint64, txSignature solana.Signature, programID solana.PublicKey,
	eventName string, innerActionHash [32]byte, eventData any) error {
	args := m.Called(tx, slot, blockNumber, txSignature, programID, eventName, eventData)

	return args.Error(0)
}

func (m *MockStorageHandler) GetBlockhashBySlot(slot uint64) (solana.Hash, error) {
	args := m.Called(slot)

	//nolint:forcetypeassert
	return args.Get(0).(solana.Hash), args.Error(1)
}

func (m *MockStorageHandler) GetBlockNumberByBlockhash(hash solana.Hash) (uint64, error) {
	args := m.Called(hash)

	//nolint:forcetypeassert
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockStorageHandler) StoreLatestBlockPoint(tx StorageTransaction, blockPoint BlockPoint) error {
	args := m.Called(tx, blockPoint)

	return args.Error(0)
}

func (m *MockStorageHandler) GetLatestBlockPoint() (*BlockPoint, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).(*BlockPoint), args.Error(1)
}

func (m *MockStorageHandler) StoreLatestFinalizedBlockNumber(blockNumber uint64) error {
	args := m.Called(blockNumber)

	return args.Error(0)
}
func (m *MockStorageHandler) GetLatestFinalizedBlockNumber() (uint64, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockStorageHandler) GetEventsBySlot(slot uint64) ([]EventRecord, error) {
	args := m.Called(slot)

	//nolint:forcetypeassert
	return args.Get(0).([]EventRecord), args.Error(1)
}

func (m *MockStorageHandler) GetProcessedTxSignaturesBySlot(slot uint64) ([]solana.Signature, error) {
	args := m.Called(slot)

	//nolint:forcetypeassert
	return args.Get(0).([]solana.Signature), args.Error(1)
}

func (m *MockStorageHandler) GetEventsByBlockNumber(blockNumber uint64) ([]EventRecord, error) {
	args := m.Called(blockNumber)

	//nolint:forcetypeassert
	return args.Get(0).([]EventRecord), args.Error(1)
}

func (m *MockStorageHandler) PushUnprocessedTransactions(txPoints []TxPoint) error {
	args := m.Called(txPoints)

	return args.Error(0)
}

func (m *MockStorageHandler) GetAllUnprocessedTransactions() ([]TxPoint, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).([]TxPoint), args.Error(1)
}

func (m *MockStorageHandler) SetLastProcessedTransaction(txPoint TxPoint) error {
	args := m.Called(txPoint)

	return args.Error(0)
}

func (m *MockStorageHandler) FinalizeProcessedTransaction(txSignature solana.Signature) error {
	args := m.Called(txSignature)

	return args.Error(0)
}

func (m *MockStorageHandler) GetLastProcessedTransaction() (TxPoint, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).(TxPoint), args.Error(1)
}

func (m *MockStorageHandler) StoreLatestQueriedTransaction(txPoint TxPoint) error {
	args := m.Called(txPoint)

	return args.Error(0)
}

func (m *MockStorageHandler) GetLatestQueriedTransaction() (TxPoint, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).(TxPoint), args.Error(1)
}
