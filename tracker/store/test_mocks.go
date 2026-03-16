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

func (m *MockStorageHandler) ReadSlot() (uint64, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockStorageHandler) StoreSlot(tx StorageTransaction, slot uint64) error {
	args := m.Called(tx, slot)

	return args.Error(0)
}

func (m *MockStorageHandler) StoreBlock(tx StorageTransaction, slot uint64, hash solana.Hash) error {
	args := m.Called(tx, slot, hash)

	return args.Error(0)
}

func (m *MockStorageHandler) StoreEvent(
	tx StorageTransaction, slot uint64, txSignature solana.Signature, programID solana.PublicKey, eventName string, eventData any) error {
	args := m.Called(tx, slot, txSignature, programID, eventName, eventData)

	return args.Error(0)
}

func (m *MockStorageHandler) UseTransactions() bool {
	args := m.Called()

	return args.Bool(0)
}

func (m *MockStorageHandler) ApplyTransaction(
	slotFn func(StorageTransaction) error, eventFns []func(StorageTransaction) error) error {
	args := m.Called(slotFn, eventFns)

	return args.Error(0)
}

func (m *MockStorageHandler) GetBlockhashBySlot(slot uint64) (solana.Hash, error) {
	args := m.Called(slot)

	//nolint:forcetypeassert
	return args.Get(0).(solana.Hash), args.Error(1)
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

func (m *MockStorageHandler) GetUnprocessedEvents(limit int) ([]EventRecord, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).([]EventRecord), args.Error(1)
}

func (m *MockStorageHandler) MarkEventAsProcessed(eventID uint64) error {
	args := m.Called(eventID)

	return args.Error(0)
}

func (m *MockStorageHandler) GetUnprocessedEventCount() (int, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).(int), args.Error(1)
}

func (m *MockStorageHandler) GetProcessedEventCount() (int, error) {
	args := m.Called()

	//nolint:forcetypeassert
	return args.Get(0).(int), args.Error(1)
}
