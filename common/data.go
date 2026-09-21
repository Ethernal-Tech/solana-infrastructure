package common

import "github.com/gagliardetto/solana-go/rpc"

type ChainID = string

const (
	ChainIDPrime   ChainID = "prime"
	ChainIDVector  ChainID = "vector"
	ChainIDNexus   ChainID = "nexus"
	ChainIDPolygon ChainID = "polygon"
	ChainIDCardano ChainID = "cardano"
	ChainIDSolana  ChainID = "solana"
)

// MaxSupportedTransactionVersion is the transaction version RPC calls that read
// blocks and transactions must ask for. Reading a version is not opt-in the way
// sending it is: a node omits or rejects anything newer than the value the
// caller states, and bridge batches are built as v1 (SIMD-0385).
func MaxSupportedTransactionVersion() *uint64 {
	version := rpc.MaxSupportedTransactionVersion1

	return &version
}
