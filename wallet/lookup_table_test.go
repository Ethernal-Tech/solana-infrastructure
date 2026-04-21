package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"
	alt "github.com/gagliardetto/solana-go/programs/address-lookup-table"
	"github.com/stretchr/testify/require"
)

type fakeALTFetcher struct {
	tables map[solana.PublicKey]*alt.AddressLookupTableState
	calls  map[solana.PublicKey]int
	err    error
}

func newFakeALTFetcher() *fakeALTFetcher {
	return &fakeALTFetcher{
		tables: make(map[solana.PublicKey]*alt.AddressLookupTableState),
		calls:  make(map[solana.PublicKey]int),
	}
}

func (f *fakeALTFetcher) GetAddressLookupTable(
	_ context.Context, address solana.PublicKey,
) (*alt.AddressLookupTableState, error) {
	f.calls[address]++

	if f.err != nil {
		return nil, f.err
	}

	return f.tables[address], nil
}

func randomKeys(t *testing.T, n int) solana.PublicKeySlice {
	t.Helper()

	out := make(solana.PublicKeySlice, n)
	for i := 0; i < n; i++ {
		out[i] = solana.NewWallet().PublicKey()
	}

	return out
}

func TestAddressLookupTableResolver_Resolve_CachesAndFetchesOnce(t *testing.T) {
	ctx := context.Background()

	altAddr := solana.NewWallet().PublicKey()
	entries := randomKeys(t, 5)

	fetcher := newFakeALTFetcher()
	fetcher.tables[altAddr] = &alt.AddressLookupTableState{Addresses: entries}

	resolver := NewAddressLookupTableResolver(fetcher)

	got, err := resolver.Resolve(ctx, altAddr)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, entries, got[altAddr])

	got, err = resolver.Resolve(ctx, altAddr)
	require.NoError(t, err)
	require.Equal(t, entries, got[altAddr])

	require.Equal(t, 1, fetcher.calls[altAddr],
		"second Resolve call should have been served from cache")
}

func TestAddressLookupTableResolver_Resolve_FetchesOnlyMissing(t *testing.T) {
	ctx := context.Background()

	warm := solana.NewWallet().PublicKey()
	cold := solana.NewWallet().PublicKey()

	fetcher := newFakeALTFetcher()
	fetcher.tables[warm] = &alt.AddressLookupTableState{Addresses: randomKeys(t, 3)}
	fetcher.tables[cold] = &alt.AddressLookupTableState{Addresses: randomKeys(t, 4)}

	resolver := NewAddressLookupTableResolver(fetcher)

	_, err := resolver.Resolve(ctx, warm)
	require.NoError(t, err)
	require.Equal(t, 1, fetcher.calls[warm])

	_, err = resolver.Resolve(ctx, warm, cold)
	require.NoError(t, err)
	require.Equal(t, 1, fetcher.calls[warm], "warm entry should not be re-fetched")
	require.Equal(t, 1, fetcher.calls[cold])
}

func TestAddressLookupTableResolver_Invalidate(t *testing.T) {
	ctx := context.Background()

	a := solana.NewWallet().PublicKey()
	b := solana.NewWallet().PublicKey()

	fetcher := newFakeALTFetcher()
	fetcher.tables[a] = &alt.AddressLookupTableState{Addresses: randomKeys(t, 2)}
	fetcher.tables[b] = &alt.AddressLookupTableState{Addresses: randomKeys(t, 2)}

	resolver := NewAddressLookupTableResolver(fetcher)
	require.NoError(t, resolver.Preload(ctx, a, b))

	resolver.Invalidate(a)

	_, err := resolver.Resolve(ctx, a, b)
	require.NoError(t, err)
	require.Equal(t, 2, fetcher.calls[a], "a should be re-fetched after invalidation")
	require.Equal(t, 1, fetcher.calls[b], "b should remain cached")

	resolver.Invalidate()

	_, err = resolver.Resolve(ctx, a, b)
	require.NoError(t, err)
	require.Equal(t, 3, fetcher.calls[a])
	require.Equal(t, 2, fetcher.calls[b])
}

func TestAddressLookupTableResolver_Resolve_MissingTable(t *testing.T) {
	ctx := context.Background()

	addr := solana.NewWallet().PublicKey()

	fetcher := newFakeALTFetcher()
	resolver := NewAddressLookupTableResolver(fetcher)

	_, err := resolver.Resolve(ctx, addr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestAddressLookupTableResolver_Resolve_FetcherError(t *testing.T) {
	ctx := context.Background()

	addr := solana.NewWallet().PublicKey()

	fetcher := newFakeALTFetcher()
	fetcher.err = errors.New("rpc boom")

	resolver := NewAddressLookupTableResolver(fetcher)

	_, err := resolver.Resolve(ctx, addr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "rpc boom")

	require.NoError(t, func() error {
		fetcher.err = nil
		fetcher.tables[addr] = &alt.AddressLookupTableState{Addresses: randomKeys(t, 1)}
		_, err := resolver.Resolve(ctx, addr)

		return err
	}(), "failed fetches must not poison the cache")
}
