package common

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

const (
	// devnetEndpoint is the public Solana devnet node every probe runs against.
	devnetEndpoint = "https://api.devnet.solana.com"

	// envRunDevnetLimits gates the probe: it spends a real, shared, public node's
	// whole request budget and then keeps asking, one method at a time, until the
	// node refuses. It never runs by default.
	//   go test ./common/... -run TestDevnetRPCMethodLimits -v -timeout 20m
	envRunDevnetLimits = "SOLANA_DEVNET_RATE_LIMIT_TEST"

	// envIncludeAirdrop additionally enables the requestAirdrop probe. It is off
	// by default because the public faucet allows a single airdrop per window
	// per IP, so hammering it only burns a shared resource and measures the
	// faucet rather than our limiter.
	envIncludeAirdrop = "SOLANA_DEVNET_RATE_LIMIT_TEST_AIRDROP"

	headerMethodLimit     = "X-Ratelimit-Method-Limit"
	headerMethodRemaining = "X-Ratelimit-Method-Remaining"
	headerRetryAfter      = "Retry-After"

	// errTextMethodLimit is how the node names a single method running out of
	// quota in the 429 body. Its other refusals ("Connection rate limits
	// exceeded", and the total request budget) come out of budgets an IP shares
	// across every method, and the limit they report says nothing about the method
	// that happened to be asking.
	errTextMethodLimit = "Too many requests for a specific RPC call"

	// devnetBlockLookback is how far behind the finalized slot the probe looks
	// for a block that is guaranteed to exist (not skipped).
	devnetBlockLookback = 10

	// probeWindow is the window every probe works in, because it is the window the
	// node counts requests per IP over: 100 in total, and fewer per method.
	probeWindow = rpcMethodLimitWindow

	// perMethodProbeRequests is what every per-method probe sends inside one
	// probeWindow: more than any per-method limit the node has (the largest seen
	// is 50), and one short of the 100 an IP gets in total, so the bucket that
	// runs out is the method's own and not the budget shared with everything else.
	perMethodProbeRequests = 99

	// probeRPSLimit is the global rate the client is built with. It only has to
	// stay out of the way: the probes pace themselves, and a caller with one
	// request in flight is bounded by round trip latency long before this.
	probeRPSLimit = 25

	// interMethodPause is the silence after every probe. It outlasts the node's
	// window, so the next probe starts with the method quota, the total request
	// budget and the Retry-After of the last refusal all cleared.
	interMethodPause = 15 * time.Second
)

// devnetLimitsConfig is the configuration under test.
var devnetLimitsConfig = &RPCMethodLimitsConfig{
	GlobalRPSLimit:          7,
	GetBalance:              38,
	GetTokenAccountBalance:  40,
	GetAccountInfo:          40,
	GetProgramAccounts:      10,
	GetLatestBlockhash:      40,
	GetSlot:                 40,
	SendTransaction:         20,
	GetSignatureStatuses:    10,
	SimulateTransaction:     40,
	RequestAirdrop:          40,
	GetTransaction:          10,
	GetSignaturesForAddress: 10,
	GetBlocks:               40,
	GetBlock:                6,
	GetBlockHeight:          40,
}

// refusal is what the node said when it turned a request down.
type refusal struct {
	methodLimit     string // X-Ratelimit-Method-Limit
	methodRemaining int    // X-Ratelimit-Method-Remaining, math.MaxInt when absent
	retryAfter      string // Retry-After
	headers         string // every X-Ratelimit-* header of the response
	message         string // the bucket the node named in the error body
}

// methodRateLimitStats accumulates what the node reported for one JSON-RPC method.
type methodRateLimitStats struct {
	requests      int           // requests that actually left the process
	rateLimited   int           // 429 responses
	roundTripSum  time.Duration // total time spent on the wire
	roundTripMax  time.Duration // slowest single round trip
	serverLimit   string        // last X-Ratelimit-Method-Limit, on any response
	lastRemaining int           // last X-Ratelimit-Method-Remaining, on any response
	minRemaining  int           // lowest X-Ratelimit-Method-Remaining seen

	// lastRefusal is the most recent 429. A caller with one request of this method
	// in flight can read it as soon as its call returns and know the refusal is
	// its own, which is how the numbers get out: they are only ever in the
	// response headers, never in the error the client returns.
	lastRefusal refusal
}

// newMethodRateLimitStats returns stats with the "nothing seen yet" sentinels in
// place, so a header the node never sent is not read as a zero the assertions act
// on.
func newMethodRateLimitStats() *methodRateLimitStats {
	return &methodRateLimitStats{
		minRemaining:  math.MaxInt,
		lastRemaining: math.MaxInt,
		lastRefusal:   refusal{methodRemaining: math.MaxInt},
	}
}

// rateLimitStatsTransport records per-method request counts, wire time and the
// X-Ratelimit-* headers of the node. The headers are the point: they carry the
// limit the node puts on a method, and they do not survive into the solana-go
// error, which only keeps the status code.
type rateLimitStatsTransport struct {
	base   http.RoundTripper
	logger hclog.Logger
	mu     sync.Mutex
	stats  map[string]*methodRateLimitStats
}

func newRateLimitStatsTransport(base http.RoundTripper, logger hclog.Logger) *rateLimitStatsTransport {
	return &rateLimitStatsTransport{
		base:   base,
		logger: logger,
		stats:  map[string]*methodRateLimitStats{},
	}
}

func (t *rateLimitStatsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	method := rpcMethodFromRequest(req)

	start := time.Now().UTC()
	resp, err := t.base.RoundTrip(req)
	elapsed := time.Since(start)

	t.mu.Lock()
	defer t.mu.Unlock()

	stats := t.stats[method]
	if stats == nil {
		stats = newMethodRateLimitStats()
		t.stats[method] = stats
	}

	stats.requests++
	stats.roundTripSum += elapsed
	stats.roundTripMax = max(stats.roundTripMax, elapsed)

	if err != nil {
		return nil, err
	}

	if limit := resp.Header.Get(headerMethodLimit); limit != "" {
		stats.serverLimit = limit
	}

	remaining := math.MaxInt

	if value, convErr := strconv.Atoi(resp.Header.Get(headerMethodRemaining)); convErr == nil {
		remaining = value
		stats.lastRemaining = value
		stats.minRemaining = min(stats.minRemaining, value)
	}

	// Unlike RateLimitLoggingTransport, a 429 is not answered by closing the
	// socket here. These probes collect refusals on purpose, and paying for a TLS
	// handshake on every one of them costs about five times the round trip
	// (~190ms against ~37ms on a connection that is kept), which is the
	// difference between spending a method's quota inside the node's window and
	// running out of window first.
	if resp.StatusCode == http.StatusTooManyRequests {
		stats.rateLimited++
		stats.lastRefusal = refusal{
			methodLimit:     resp.Header.Get(headerMethodLimit),
			methodRemaining: remaining,
			retryAfter:      resp.Header.Get(headerRetryAfter),
			headers:         formatRateLimitHeaders(resp.Header),
		}

		t.logRefusal(req, resp.Header)
	}

	return resp, nil
}

// logRefusal reports one 429 the way RateLimitLoggingTransport reports it in
// production, so a probe run reads like a client run does: every refusal, naming
// the buckets the node put in it, as it happens.
func (t *rateLimitStatsTransport) logRefusal(req *http.Request, h http.Header) {
	t.logger.Warn("rpc request rate limited (429)",
		"method", rpcMethodFromRequest(req),
		"host", req.URL.Host,
		"methodLimit", h.Get("X-Ratelimit-Method-Limit"),
		"methodRemaining", h.Get("X-Ratelimit-Method-Remaining"),
		"rpsLimit", h.Get("X-Ratelimit-Rps-Limit"),
		"rpsRemaining", h.Get("X-Ratelimit-Rps-Remaining"),
		"connLimit", h.Get("X-Ratelimit-Conn-Limit"),
		"connRemaining", h.Get("X-Ratelimit-Conn-Remaining"),
		"retryAfter", h.Get("Retry-After"),
	)
}

// testLogWriter routes the transport's log lines into the test output, so the
// refusals show up in the run next to the numbers the probes print.
type testLogWriter struct {
	t *testing.T
}

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))

	return len(p), nil
}

// formatRateLimitHeaders renders every X-Ratelimit-* header of a response, so a
// 429 can be traced back to the bucket that produced it: the per-method one, or
// one of the node's global buckets (rps, endpoint, connection rate).
func formatRateLimitHeaders(header http.Header) string {
	values := make([]string, 0, len(header))

	for name, value := range header {
		if !strings.HasPrefix(http.CanonicalHeaderKey(name), "X-Ratelimit-") {
			continue
		}

		values = append(values, fmt.Sprintf("%s=%s", name, strings.Join(value, ",")))
	}

	sort.Strings(values)

	return strings.Join(values, " ")
}

func (t *rateLimitStatsTransport) snapshot(method string) methodRateLimitStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	if stats := t.stats[method]; stats != nil {
		return *stats
	}

	return *newMethodRateLimitStats()
}

// reset drops the counters of a method so each probe measures its own window.
func (t *rateLimitStatsTransport) reset(method string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.stats, method)
}

// refusalMessage returns the message the node put in a 429 body, which is where
// it names the bucket that refused the request. The error prints as a multi line
// dump, so the message is read off the typed error instead.
func refusalMessage(err error) string {
	if err == nil {
		return ""
	}

	var rpcErr *jsonrpc.RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Message
	}

	return err.Error()
}

// isMethodLimitRefusal reports whether err is the node refusing a request because
// that one method ran out of quota, as opposed to a 429 out of a budget the
// method shares with every other one.
func isMethodLimitRefusal(err error) bool {
	if _, is429 := rateLimitRetryAfter(err); !is429 {
		return false
	}

	return strings.Contains(refusalMessage(err), errTextMethodLimit)
}

// devnetFixtures holds the real devnet values the probes send as parameters, so
// every method gets a genuine response instead of an "invalid params" error.
type devnetFixtures struct {
	account      solana.PublicKey // a real, funded account
	tokenAccount solana.PublicKey // a real SPL token account (zero when undiscoverable)
	signature    solana.Signature // a real, finalized transaction signature
	blockSlot    uint64           // a finalized, non-skipped slot
	unfundedTx   *solana.Transaction
}

// devnetMethodCase describes one probe: which method it drives, the limit the
// configuration puts on it, and how to call it with real parameters. call
// returns a short description of the response for the log.
type devnetMethodCase struct {
	method     string
	limit      int
	skipReason string
	call       func(ctx context.Context, client *rpc.Client) (string, error)
}

// methodProbeResult is what one method probe observed, and one row of the
// summary table logged at the end.
type methodProbeResult struct {
	method    string
	limit     int
	succeeded int
	failed    int

	// served is how many requests the node answered before it first refused one,
	// whatever the answer was: the limit as the probe experienced it, next to the
	// one the node reports.
	served int

	// methodRefusals counts the 429s that named this method's own limit, and
	// otherRefusals the ones out of a budget shared across methods.
	methodRefusals int
	otherRefusals  int

	elapsed   time.Duration
	firstResp string
	lastErr   error

	// methodRefusal is the first refusal that named the method's own limit, and
	// the only one the check reads a number out of.
	methodRefusal  refusal
	sawMethodLimit bool

	stats methodRateLimitStats
}

// TestDevnetRPCMethodLimits checks the per-method limits in devnetLimitsConfig
// against the ones the public devnet node actually enforces.
//
// Every method gets perMethodProbeRequests requests inside one window - past
// every per-method limit the node has - and the refusal that comes back names
// the method's real limit in its X-Ratelimit-Method-Limit header. That number is
// the floor the configuration is held to: a configured limit below it leaves
// throughput the node was willing to give, which is what the check fails on.
//
// Every subtest is followed by interMethodPause of silence, so the node's
// counters and the Retry-After of the last refusal are clear before the next one
// starts.
//
// The probes drive the plain rpc.Client rather than a MutexRPCClient on purpose:
// the per-method limiter is configured at or below what the node allows and
// retries a 429 internally, so a caller going through it is never refused and
// there would be no reported limit to read.
func TestDevnetRPCMethodLimits(t *testing.T) {
	if os.Getenv(envRunDevnetLimits) == "" {
		t.Skipf("set %s=1 to run the devnet rate limit probe (it spends a public node's request budget)",
			envRunDevnetLimits)
	}

	logger := hclog.New(&hclog.LoggerOptions{
		Name:   "devnet-probe",
		Level:  hclog.Warn,
		Output: testLogWriter{t: t},
	})

	transport := newRateLimitStatsTransport(devnetHTTPTransport(), logger)
	rpcClient := newDevnetRPCClient(transport, probeRPSLimit)

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), time.Minute)
	defer cancelSetup()

	fixtures := discoverDevnetFixtures(t, setupCtx, rpcClient)
	t.Logf("fixtures: account=%s tokenAccount=%s signature=%s blockSlot=%d",
		fixtures.account, fixtures.tokenAccount, fixtures.signature, fixtures.blockSlot)

	cases := devnetMethodCases(devnetLimitsConfig, fixtures)
	results := make([]methodProbeResult, 0, len(cases))

	// Discovering the fixtures spent requests out of the window the first probe is
	// about to fill, so let the node's counters clear before it starts.
	time.Sleep(interMethodPause)

	for _, testCase := range cases {
		t.Run(testCase.method, func(t *testing.T) {
			if testCase.skipReason != "" {
				t.Skip(testCase.skipReason)
			}

			// Hand the node its window back before the next method asks for it.
			t.Cleanup(func() { time.Sleep(interMethodPause) })

			// Record before asserting: a failed assertion aborts the subtest, and
			// the summary should still show what the probe measured.
			result := probeMethodLimit(t, testCase, rpcClient, transport)
			results = append(results, result)

			assertMethodLimit(t, result)
		})
	}

	logProbeSummary(t, results)
}

// probeMethodLimit sends perMethodProbeRequests calls for one method inside
// probeWindow and reports every refusal that came back.
//
// It does not stop at the first one. The number the check needs is in the header
// of a refusal that names the method's own limit, and a public node also refuses
// out of buckets that have nothing to do with the method, so the probe keeps
// asking for the whole window and looks for the refusal it came for.
func probeMethodLimit(
	t *testing.T,
	testCase devnetMethodCase,
	client *rpc.Client,
	transport *rateLimitStatsTransport,
) methodProbeResult {
	t.Helper()

	transport.reset(testCase.method)

	ctx, cancel := context.WithTimeout(context.Background(), probeWindow)
	defer cancel()

	result := methodProbeResult{
		method:        testCase.method,
		limit:         testCase.limit,
		methodRefusal: refusal{methodRemaining: math.MaxInt},
	}

	start := time.Now().UTC()

	// One request at a time, on purpose. It is what makes result.served exact: a
	// second request in flight when the first refusal comes back has already been
	// counted by the node, so the number of requests it served before saying no
	// would be a range rather than a number, and that number is the whole
	// cross-check on the limit it reports.
	for sent := 0; sent < perMethodProbeRequests && ctx.Err() == nil; sent++ {
		response, err := testCase.call(ctx, client)

		if _, is429 := rateLimitRetryAfter(err); !is429 {
			if result.methodRefusals+result.otherRefusals == 0 {
				result.served++
			}

			if err != nil {
				result.failed++
				result.lastErr = err

				continue
			}

			result.succeeded++

			if result.firstResp == "" {
				result.firstResp = response
			}

			continue
		}

		result.lastErr = err

		if !isMethodLimitRefusal(err) {
			result.otherRefusals++

			continue
		}

		result.methodRefusals++

		if !result.sawMethodLimit {
			// The transport recorded the headers of this very response, and with
			// one request of this method in flight it can be no other refusal.
			result.sawMethodLimit = true
			result.methodRefusal = transport.snapshot(testCase.method).lastRefusal
			result.methodRefusal.message = refusalMessage(err)
		}
	}

	result.elapsed = time.Since(start)
	result.stats = transport.snapshot(testCase.method)

	t.Logf("%s: configured=%d requests=%d served=%d ok=%d failed=%d method429s=%d other429s=%d "+
		"elapsed=%s wire=%s slowest=%s reportedLimit=%s remainingOn429=%s retryAfter=%s",
		result.method, result.limit, result.stats.requests, result.served, result.succeeded, result.failed,
		result.methodRefusals, result.otherRefusals,
		result.elapsed.Round(time.Millisecond), result.stats.roundTripSum.Round(time.Millisecond),
		result.stats.roundTripMax.Round(time.Millisecond),
		orNA(result.methodRefusal.methodLimit), remainingOrNA(result.methodRefusal.methodRemaining),
		orNA(result.methodRefusal.retryAfter))

	if result.firstResp != "" {
		t.Logf("%s: response: %s", result.method, result.firstResp)
	}

	if result.lastErr != nil {
		t.Logf("%s: last error: %s", result.method, refusalMessage(result.lastErr))
	}

	return result
}

// reportedMethodLimit returns the limit the node named for the method and where
// that number came from.
//
// Being refused for the method itself is the direct answer, but a method whose
// own limit is above the budget an IP shares across methods can never produce
// that refusal: the shared budget runs out first, whatever the method. The node
// puts the same X-Ratelimit-Method-Limit header on the responses it serves, so
// that is the fallback, and it is the only reading available for those methods.
func reportedMethodLimit(result methodProbeResult) (value string, fromRefusal bool) {
	if result.sawMethodLimit {
		return result.methodRefusal.methodLimit, true
	}

	return result.stats.serverLimit, false
}

// assertMethodLimit checks the limit the node names for a method against the
// limit the configuration puts on it.
func assertMethodLimit(t *testing.T, result methodProbeResult) {
	t.Helper()

	value, fromRefusal := reportedMethodLimit(result)

	require.NotEmpty(t, value,
		"%s: the node named no limit for this method in %d requests over %s: it neither refused the method "+
			"itself nor sent an %s header (%d answered before the first refusal, %d refusals out of other "+
			"buckets, last refusal: %s)",
		result.method, result.stats.requests, result.elapsed.Round(time.Millisecond), headerMethodLimit,
		result.served, result.otherRefusals, orNA(refusalMessage(result.lastErr)))

	reported, err := strconv.Atoi(value)
	require.NoError(t, err, "%s: the node named a non numeric method limit %q", result.method, value)

	switch {
	case !fromRefusal:
		t.Logf("%s: never refused for its own limit of %d, because the budget shared across methods ran "+
			"out first after %d requests, so the limit is the one the node advertises rather than one it "+
			"enforced here (%d refusals out of other buckets)",
			result.method, reported, result.served, result.otherRefusals)
	case result.served != reported:
		// What the node reports and what it served should be the same number. A gap
		// means the window rolled over mid probe, or something else behind this IP
		// spent part of the quota.
		t.Logf("%s: the node reports a limit of %d but answered %d requests before refusing one",
			result.method, reported, result.served)
	}

	// Only the one direction is a failure. A configured limit under what the node
	// allows throttles the client below what it could have had, which is the thing
	// this run is looking for. Above it is left to the summary: the configuration
	// also targets endpoints other than this public node, which hands out its own
	// numbers.
	require.GreaterOrEqual(t, reported, result.limit,
		"%s: the node allows %d requests per %s, the configuration only allows %d, so the client throttles "+
			"itself %d requests per window below what this node was willing to serve",
		result.method, reported, probeWindow, result.limit, reported-result.limit)
}

// logProbeSummary prints the configured limit of every method next to the limit
// the node reported when it refused, so one run names every method the
// configuration has the wrong number for, not just the first one that failed.
func logProbeSummary(t *testing.T, results []methodProbeResult) {
	t.Helper()

	var summary strings.Builder

	summary.WriteString(
		"\nmethod                    configured  reported  source  served  method429s  other429s\n")

	for _, result := range results {
		value, fromRefusal := reportedMethodLimit(result)

		source := "header"
		if fromRefusal {
			source = "429"
		}

		summary.WriteString(fmt.Sprintf("%-25s %10d  %8s  %6s  %6d  %10d  %9d\n",
			result.method, result.limit, orNA(value), source, result.served,
			result.methodRefusals, result.otherRefusals))
	}

	t.Log(summary.String())

	for _, result := range results {
		value, _ := reportedMethodLimit(result)

		reported, err := strconv.Atoi(value)
		if err != nil {
			continue
		}

		switch {
		case result.limit < reported:
			t.Logf("UNDER: %s is configured for %d requests per %s, the node allows %d",
				result.method, result.limit, probeWindow, reported)
		case result.limit > reported:
			// Not a failure, but worth naming: against this node those calls run out
			// of quota before the client's own limiter has finished handing it out.
			t.Logf("OVER: %s is configured for %d requests per %s, this node allows only %d",
				result.method, result.limit, probeWindow, reported)
		}
	}
}

// devnetMethodCases builds one probe per rate limited method. Every call uses
// real devnet data; the ones that would change state (sendTransaction,
// simulateTransaction) use a throwaway key with no lamports, so the node always
// rejects them and nothing ever lands on chain.
func devnetMethodCases(config *RPCMethodLimitsConfig, fixtures devnetFixtures) []devnetMethodCase {
	var (
		maxTxVersion    = uint64(0)
		signatureLimit  = 1
		includeRewards  = false
		emptyDataLength = uint64(0)
		dataSliceOffset = uint64(0)
	)

	airdropSkip := ""
	if os.Getenv(envIncludeAirdrop) == "" {
		airdropSkip = fmt.Sprintf(
			"set %s=1 to probe requestAirdrop: the public faucet allows one airdrop per window per IP, "+
				"so this measures the faucet rather than the client limiter", envIncludeAirdrop)
	}

	return []devnetMethodCase{
		{
			method: rpcMethodGetBalance,
			limit:  config.GetBalance,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetBalance(ctx, fixtures.account, rpc.CommitmentFinalized)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("lamports=%d slot=%d", result.Value, result.Context.Slot), nil
			},
		},
		{
			method: rpcMethodGetTokenAccountBalance,
			limit:  config.GetTokenAccountBalance,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetTokenAccountBalance(ctx, fixtures.tokenAccount, rpc.CommitmentFinalized)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("amount=%s decimals=%d", result.Value.Amount, result.Value.Decimals), nil
			},
		},
		{
			method: rpcMethodGetAccountInfo,
			limit:  config.GetAccountInfo,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetAccountInfoWithOpts(ctx, fixtures.account, &rpc.GetAccountInfoOpts{
					Encoding:   solana.EncodingBase64,
					Commitment: rpc.CommitmentFinalized,
				})
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("owner=%s lamports=%d dataLen=%d",
					result.Value.Owner, result.Value.Lamports, len(result.Value.Data.GetBinary())), nil
			},
		},
		{
			method: rpcMethodGetProgramAccounts,
			limit:  config.GetProgramAccounts,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				// A token account layout filtered by an owner that holds none:
				// a real, cheap query that returns an empty set.
				result, err := client.GetProgramAccountsWithOpts(ctx, solana.TokenProgramID, &rpc.GetProgramAccountsOpts{
					Encoding:   solana.EncodingBase64,
					Commitment: rpc.CommitmentFinalized,
					DataSlice:  &rpc.DataSlice{Offset: &dataSliceOffset, Length: &emptyDataLength},
					Filters: []rpc.RPCFilter{
						{DataSize: 165},
						{Memcmp: &rpc.RPCFilterMemcmp{Offset: 32, Bytes: fixtures.account.Bytes()}},
					},
				})
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("accounts=%d", len(result)), nil
			},
		},
		{
			method: rpcMethodGetLatestBlockhash,
			limit:  config.GetLatestBlockhash,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("blockhash=%s lastValidBlockHeight=%d",
					result.Value.Blockhash, result.Value.LastValidBlockHeight), nil
			},
		},
		{
			method: rpcMethodGetSlot,
			limit:  config.GetSlot,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				slot, err := client.GetSlot(ctx, rpc.CommitmentFinalized)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("slot=%d", slot), nil
			},
		},
		{
			method: rpcMethodSendTransaction,
			limit:  config.SendTransaction,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				// Preflight always rejects this transaction (the payer has no
				// lamports), so the response is real but nothing is submitted.
				signature, err := client.SendTransactionWithOpts(ctx, fixtures.unfundedTx, rpc.TransactionOpts{
					PreflightCommitment: rpc.CommitmentFinalized,
				})
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("signature=%s", signature), nil
			},
		},
		{
			method: rpcMethodGetSignatureStatuses,
			limit:  config.GetSignatureStatuses,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetSignatureStatuses(ctx, true, fixtures.signature)
				if err != nil {
					return "", err
				}

				status := result.Value[0]
				if status == nil {
					return "status=unknown", nil
				}

				return fmt.Sprintf("slot=%d confirmationStatus=%s", status.Slot, status.ConfirmationStatus), nil
			},
		},
		{
			method: rpcMethodSimulateTransaction,
			limit:  config.SimulateTransaction,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.SimulateTransactionWithOpts(ctx, fixtures.unfundedTx, &rpc.SimulateTransactionOpts{
					Commitment:             rpc.CommitmentFinalized,
					ReplaceRecentBlockhash: true,
				})
				if err != nil {
					return "", err
				}

				unitsConsumed := uint64(0)
				if result.Value.UnitsConsumed != nil {
					unitsConsumed = *result.Value.UnitsConsumed
				}

				return fmt.Sprintf("err=%v unitsConsumed=%d logs=%d",
					result.Value.Err, unitsConsumed, len(result.Value.Logs)), nil
			},
		},
		{
			method:     rpcMethodRequestAirdrop,
			limit:      config.RequestAirdrop,
			skipReason: airdropSkip,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				signature, err := client.RequestAirdrop(ctx, solana.NewWallet().PublicKey(), 1, rpc.CommitmentFinalized)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("signature=%s", signature), nil
			},
		},
		{
			method: rpcMethodGetTransaction,
			limit:  config.GetTransaction,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetTransaction(ctx, fixtures.signature, &rpc.GetTransactionOpts{
					Encoding:                       solana.EncodingBase64,
					Commitment:                     rpc.CommitmentFinalized,
					MaxSupportedTransactionVersion: &maxTxVersion,
				})
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("slot=%d fee=%d", result.Slot, result.Meta.Fee), nil
			},
		},
		{
			method: rpcMethodGetSignaturesForAddress,
			limit:  config.GetSignaturesForAddress,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetSignaturesForAddressWithOpts(ctx, solana.TokenProgramID,
					&rpc.GetSignaturesForAddressOpts{
						Limit:      &signatureLimit,
						Commitment: rpc.CommitmentFinalized,
					})
				if err != nil {
					return "", err
				}

				if len(result) == 0 {
					return "signatures=0", nil
				}

				return fmt.Sprintf("signature=%s slot=%d", result[0].Signature, result[0].Slot), nil
			},
		},
		{
			method: rpcMethodGetBlocks,
			limit:  config.GetBlocks,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				endSlot := fixtures.blockSlot

				result, err := client.GetBlocks(ctx, endSlot-devnetBlockLookback, &endSlot, rpc.CommitmentFinalized)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("blocks=%d", len(result)), nil
			},
		},
		{
			method: rpcMethodGetBlock,
			limit:  config.GetBlock,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				result, err := client.GetBlockWithOpts(ctx, fixtures.blockSlot, &rpc.GetBlockOpts{
					Encoding:                       solana.EncodingBase64,
					TransactionDetails:             rpc.TransactionDetailsNone,
					Rewards:                        &includeRewards,
					Commitment:                     rpc.CommitmentFinalized,
					MaxSupportedTransactionVersion: &maxTxVersion,
				})
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("blockhash=%s blockHeight=%d", result.Blockhash, *result.BlockHeight), nil
			},
		},
		{
			method: rpcMethodGetBlockHeight,
			limit:  config.GetBlockHeight,
			call: func(ctx context.Context, client *rpc.Client) (string, error) {
				height, err := client.GetBlockHeight(ctx, rpc.CommitmentFinalized)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("blockHeight=%d", height), nil
			},
		},
	}
}

// discoverDevnetFixtures reads the values the probes need out of devnet itself,
// so the test never depends on hardcoded accounts that may disappear.
func discoverDevnetFixtures(t *testing.T, ctx context.Context, client *rpc.Client) devnetFixtures {
	t.Helper()

	slot, err := client.GetSlot(ctx, rpc.CommitmentFinalized)
	require.NoError(t, err)

	blocks, err := client.GetBlocks(ctx, slot-devnetBlockLookback, &slot, rpc.CommitmentFinalized)
	require.NoError(t, err)
	require.NotEmpty(t, blocks)

	signatureLimit := 1

	signatures, err := client.GetSignaturesForAddressWithOpts(ctx, solana.TokenProgramID,
		&rpc.GetSignaturesForAddressOpts{
			Limit:      &signatureLimit,
			Commitment: rpc.CommitmentFinalized,
		})
	require.NoError(t, err)
	require.NotEmpty(t, signatures)

	fixtures := devnetFixtures{
		account:    solana.SystemProgramID,
		signature:  signatures[0].Signature,
		blockSlot:  blocks[len(blocks)-1],
		unfundedTx: buildUnfundedTransaction(t, ctx, client),
	}

	// That token program transaction also points at a real SPL token account,
	// which is what getTokenAccountBalance needs.
	fixtures.tokenAccount = tokenAccountFromTransaction(t, ctx, client, fixtures.signature)
	if !fixtures.tokenAccount.IsZero() {
		fixtures.account = fixtures.tokenAccount
	} else {
		t.Log("no SPL token account discovered; getTokenAccountBalance will report invalid account errors")
	}

	return fixtures
}

// tokenAccountFromTransaction pulls a token account out of the balances a
// finalized token program transaction touched. Returns the zero key when the
// transaction cannot be decoded or resolves its accounts through a lookup table.
func tokenAccountFromTransaction(
	t *testing.T,
	ctx context.Context,
	client *rpc.Client,
	signature solana.Signature,
) solana.PublicKey {
	t.Helper()

	maxTxVersion := uint64(0)

	result, err := client.GetTransaction(ctx, signature, &rpc.GetTransactionOpts{
		Encoding:                       solana.EncodingBase64,
		Commitment:                     rpc.CommitmentFinalized,
		MaxSupportedTransactionVersion: &maxTxVersion,
	})
	if err != nil || result.Meta == nil {
		t.Logf("token account discovery failed: %v", err)

		return solana.PublicKey{}
	}

	transaction, err := result.Transaction.GetTransaction()
	if err != nil {
		t.Logf("token account discovery failed to decode transaction: %v", err)

		return solana.PublicKey{}
	}

	for _, balance := range result.Meta.PostTokenBalances {
		if int(balance.AccountIndex) < len(transaction.Message.AccountKeys) {
			return transaction.Message.AccountKeys[balance.AccountIndex]
		}
	}

	return solana.PublicKey{}
}

// buildUnfundedTransaction signs a 1 lamport transfer from a throwaway key that
// holds nothing. The node rejects it in preflight and in simulation, which is
// what the sendTransaction and simulateTransaction probes want: real responses,
// no state change, no devnet funds spent.
func buildUnfundedTransaction(t *testing.T, ctx context.Context, client *rpc.Client) *solana.Transaction {
	t.Helper()

	blockhash, err := client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	require.NoError(t, err)

	payer := solana.NewWallet()
	recipient := solana.NewWallet()

	transaction, err := solana.NewTransaction(
		[]solana.Instruction{
			system.NewTransferInstruction(1, payer.PublicKey(), recipient.PublicKey()).Build(),
		},
		blockhash.Value.Blockhash,
		solana.TransactionPayer(payer.PublicKey()),
	)
	require.NoError(t, err)

	_, err = transaction.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(payer.PublicKey()) {
			return &payer.PrivateKey
		}

		return nil
	})
	require.NoError(t, err)

	return transaction
}

// devnetHTTPTransport mirrors the transport NewRateLimitedRPCClient builds, so
// the probe measures the same HTTP behaviour production uses.
func devnetHTTPTransport() http.RoundTripper {
	return &http.Transport{
		IdleConnTimeout:       defaultHTTPTimeout,
		MaxIdleConns:          defaultMaxIdleConnsPerHost,
		MaxConnsPerHost:       defaultMaxIdleConnsPerHost,
		MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
}

func newDevnetRPCClient(transport http.RoundTripper, globalRPS int) *rpc.Client {
	httpClient := &http.Client{
		Timeout:   defaultHTTPTimeout,
		Transport: transport,
	}

	jsonRPCClient := jsonrpc.NewClientWithOpts(devnetEndpoint, &jsonrpc.RPCClientOpts{
		HTTPClient: httpClient,
	})

	return rpc.NewWithCustomRPCClient(NewRateLimitedJSONRPCClient(jsonRPCClient, globalRPS))
}

func orNA(value string) string {
	if value == "" {
		return "n/a"
	}

	return value
}

func remainingOrNA(remaining int) string {
	if remaining == math.MaxInt {
		return "n/a"
	}

	return strconv.Itoa(remaining)
}
