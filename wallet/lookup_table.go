package wallet

import (
	"context"
	"fmt"
	"sync"

	"github.com/gagliardetto/solana-go"
)

// AddressLookupTableResolver fetches and caches Address Lookup Table (ALT)
// contents so transactions can be built against them without hitting RPC on
// every call.
//
// The cache stores the resolved address slice per ALT account. ALT contents
// can change over time (extend / deactivate), so callers that keep a resolver
// alive for a long time should call Invalidate when they know an ALT was
// modified, or simply rebuild the resolver.
type AddressLookupTableResolver struct {
	fetcher IAddressLookupTableFetcher

	mu    sync.RWMutex
	cache map[solana.PublicKey]solana.PublicKeySlice
}

func NewAddressLookupTableResolver(fetcher IAddressLookupTableFetcher) *AddressLookupTableResolver {
	return &AddressLookupTableResolver{
		fetcher: fetcher,
		cache:   make(map[solana.PublicKey]solana.PublicKeySlice),
	}
}

// Resolve returns a map of ALT address -> addresses stored in it, suitable for
// passing into solana.TransactionAddressTables. Missing entries are fetched
// from the backing provider and cached; already-cached entries are reused.
func (r *AddressLookupTableResolver) Resolve(
	ctx context.Context, addresses ...solana.PublicKey,
) (map[solana.PublicKey]solana.PublicKeySlice, error) {
	out := make(map[solana.PublicKey]solana.PublicKeySlice, len(addresses))

	r.mu.RLock()

	missing := make([]solana.PublicKey, 0, len(addresses))

	for _, addr := range addresses {
		if entries, ok := r.cache[addr]; ok {
			out[addr] = entries

			continue
		}

		missing = append(missing, addr)
	}

	r.mu.RUnlock()

	if len(missing) == 0 {
		return out, nil
	}

	fetched := make(map[solana.PublicKey]solana.PublicKeySlice, len(missing))

	for _, addr := range missing {
		state, err := r.fetcher.GetAddressLookupTable(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch address lookup table %s: %w", addr.String(), err)
		}

		if state == nil {
			return nil, fmt.Errorf("address lookup table %s not found", addr.String())
		}

		entries := append(solana.PublicKeySlice(nil), state.Addresses...)
		fetched[addr] = entries
		out[addr] = entries
	}

	r.mu.Lock()
	for addr, entries := range fetched {
		r.cache[addr] = entries
	}
	r.mu.Unlock()

	return out, nil
}

// Preload fetches and caches the provided ALT addresses. It is equivalent to
// calling Resolve and discarding the result; useful during application
// bootstrap to warm the cache.
func (r *AddressLookupTableResolver) Preload(
	ctx context.Context, addresses ...solana.PublicKey,
) error {
	_, err := r.Resolve(ctx, addresses...)

	return err
}

// Invalidate removes the given ALT addresses from the cache so the next
// Resolve call re-fetches them. Passing no addresses clears the whole cache.
func (r *AddressLookupTableResolver) Invalidate(addresses ...solana.PublicKey) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(addresses) == 0 {
		r.cache = make(map[solana.PublicKey]solana.PublicKeySlice)

		return
	}

	for _, addr := range addresses {
		delete(r.cache, addr)
	}
}
