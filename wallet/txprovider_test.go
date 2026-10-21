package wallet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/require"
)

// newEmptyResultProvider returns a provider backed by an RPC server that answers
// every method with the raw JSON in results, or null when the method is not listed.
func newEmptyResultProvider(t *testing.T, results map[string]string) *Provider {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}

		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		result, ok := results[req.Method]
		if !ok {
			result = "null"
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":` + result + `}`))
	}))
	t.Cleanup(server.Close)

	provider, err := NewProvider(server.URL, nil)
	require.NoError(t, err)

	return provider
}

func TestProviderEmptyRPCResults(t *testing.T) {
	ctx := context.Background()
	key := solana.NewWallet().PublicKey()
	tx := &solana.Transaction{Message: solana.Message{RecentBlockhash: solana.Hash{1}}}
	tx.Signatures = []solana.Signature{{1}}

	calls := map[string]func(p *Provider) error{
		"GetBalance": func(p *Provider) error {
			_, err := p.GetBalance(ctx, key)

			return err
		},
		"GetTokenAccountBalance": func(p *Provider) error {
			_, err := p.GetTokenAccountBalance(ctx, key)

			return err
		},
		"GetAccountInfo": func(p *Provider) error {
			_, err := p.GetAccountInfo(ctx, key)

			return err
		},
		"GetProgramAccounts": func(p *Provider) error {
			_, err := p.GetProgramAccounts(ctx, key, &rpc.GetProgramAccountsOpts{})

			return err
		},
		"GetLatestBlockhash": func(p *Provider) error {
			_, err := p.GetLatestBlockhash(ctx)

			return err
		},
		"GetLatestBlockhashAndExpiry": func(p *Provider) error {
			_, err := p.GetLatestBlockhashAndExpiry(ctx)

			return err
		},
		"GetBlock": func(p *Provider) error {
			_, err := p.GetBlock(ctx, 1)

			return err
		},
		"SendTransaction": func(p *Provider) error {
			_, err := p.SendTransaction(ctx, tx)

			return err
		},
		"GetSignatureStatus": func(p *Provider) error {
			_, err := p.GetSignatureStatus(ctx, solana.Signature{1})

			return err
		},
		"GetTransaction": func(p *Provider) error {
			_, err := p.GetTransaction(ctx, solana.Signature{1})

			return err
		},
		"GetSignaturesForAddress": func(p *Provider) error {
			_, err := p.GetSignaturesForAddress(ctx, key, 10)

			return err
		},
		"GetAddressLookupTable": func(p *Provider) error {
			_, err := p.GetAddressLookupTable(ctx, key)

			return err
		},
		"SimulateTransaction": func(p *Provider) error {
			_, err := p.SimulateTransaction(ctx, tx)

			return err
		},
		"RequestSolAirdrop": func(p *Provider) error {
			_, err := p.RequestSolAirdrop(ctx, key, 1)

			return err
		},
	}

	t.Run("null result", func(t *testing.T) {
		provider := newEmptyResultProvider(t, nil)

		// A null list decodes to an empty slice, which is a valid empty answer.
		emptyListMethods := map[string]bool{"GetProgramAccounts": true, "GetSignaturesForAddress": true}

		for name, call := range calls {
			t.Run(name, func(t *testing.T) {
				require.NotPanics(t, func() {
					if emptyListMethods[name] {
						require.NoError(t, call(provider))
					} else {
						require.Error(t, call(provider))
					}
				})
			})
		}
	})

	t.Run("null nested value", func(t *testing.T) {
		const nullValue = `{"context":{"slot":1},"value":null}`

		provider := newEmptyResultProvider(t, map[string]string{
			"getTokenAccountBalance":  nullValue,
			"getAccountInfo":          nullValue,
			"getLatestBlockhash":      nullValue,
			"getSignatureStatuses":    nullValue,
			"simulateTransaction":     nullValue,
			"getProgramAccounts":      `[null]`,
			"getSignaturesForAddress": `[null]`,
		})

		for _, name := range []string{
			"GetTokenAccountBalance", "GetAccountInfo", "GetLatestBlockhash", "GetLatestBlockhashAndExpiry",
			"GetSignatureStatus", "SimulateTransaction", "GetProgramAccounts", "GetSignaturesForAddress",
		} {
			t.Run(name, func(t *testing.T) {
				require.NotPanics(t, func() {
					require.Error(t, calls[name](provider))
				})
			})
		}
	})

	t.Run("program account without data", func(t *testing.T) {
		provider := newEmptyResultProvider(t, map[string]string{
			"getProgramAccounts": `[{"pubkey":"` + key.String() + `","account":null}]`,
		})

		require.NotPanics(t, func() {
			require.Error(t, calls["GetProgramAccounts"](provider))
		})
	})
}
