package trackertest

import (
	"context"
	"testing"

	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/require"
)

// TestSimulatedGetSlot checks the mocked getSlot answers with the slot the chain is actually
// on, including when that slot carries no block, and that it tracks the chain as it moves.
func TestSimulatedGetSlot(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HistorySlots = 0

	sim := NewChainSimulator(t, cfg)
	client := sim.StartRPCServer()
	ctx := context.Background()

	sim.ProduceSlotsWithBlocks(10)

	slot, err := client.GetSlot(ctx, rpc.CommitmentConfirmed)
	require.NoError(t, err)
	require.Equal(t, sim.CurrentHeadSlot(), slot)

	// A run of empty slots still moves the chain on, so getSlot has to move with it even
	// though there is no block to be found at the slot it reports.
	sim.ProduceEmptySlots(20)

	slot, err = client.GetSlot(ctx, rpc.CommitmentConfirmed)
	require.NoError(t, err)
	require.Equal(t, sim.CurrentHeadSlot(), slot)
	require.Nil(t, sim.BlockAt(slot), "the reported slot was supposed to be an empty one")

	blockHeight, err := client.GetBlockHeight(ctx, rpc.CommitmentConfirmed)
	require.NoError(t, err)
	require.Less(t, blockHeight, slot, "block height counts blocks, so it has to trail the slot")
}
