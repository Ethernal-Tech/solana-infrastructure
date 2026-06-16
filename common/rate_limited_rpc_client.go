package common

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"github.com/hashicorp/go-hclog"
	"go.uber.org/ratelimit"
)

// RateLimitLoggingTransport is an http.RoundTripper that logs the rate-limit
// response headers whenever the RPC node responds with 429 Too Many Requests.
//
// The headers cannot be recovered from the error returned by the solana-go
// client (only the status code survives as *jsonrpc.HTTPError), so they have
// to be captured here, at the HTTP transport level.
type RateLimitLoggingTransport struct {
	// Base is the underlying transport. http.DefaultTransport is used when nil.
	Base http.RoundTripper
	// Logger used for 429 reports. hclog.Default() is used when nil.
	Logger hclog.Logger
}

func (t *RateLimitLoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	resp, err := base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusTooManyRequests {
		return resp, err
	}

	logger := t.Logger
	if logger == nil {
		logger = hclog.Default()
	}

	h := resp.Header
	logger.Warn("rpc request rate limited (429)",
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

	// FIX: To prevent OS socket accumulation and connection pool leaks
	// during a rate limit storm, instruct the HTTP client to close this
	// specific socket immediately instead of recycling it back to the pool.
	resp.Header.Set("Connection", "close")
	resp.Close = true

	return resp, nil
}

// rpcMethodFromRequest extracts the JSON-RPC method name(s) from the request
// body. The solana-go client builds requests from a *bytes.Reader, so
// req.GetBody can replay the body without disturbing the original request.
// Returns "" when the body cannot be re-read or parsed. Only called on 429
// responses, so the extra body read is negligible.
func rpcMethodFromRequest(req *http.Request) string {
	if req.GetBody == nil {
		return ""
	}

	body, err := req.GetBody()
	if err != nil {
		return ""
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, 64*1024))
	if err != nil {
		return ""
	}

	var single struct {
		Method string `json:"method"`
	}

	if err := json.Unmarshal(raw, &single); err == nil && single.Method != "" {
		return single.Method
	}

	// Batch requests carry an array of JSON-RPC calls.
	var batch []struct {
		Method string `json:"method"`
	}

	if err := json.Unmarshal(raw, &batch); err == nil && len(batch) > 0 {
		methods := make([]string, 0, len(batch))
		for _, r := range batch {
			methods = append(methods, r.Method)
		}

		return strings.Join(methods, ",")
	}

	return ""
}

// rateLimitedJSONRPCClient mirrors solana-go's unexported clientWithRateLimiting
// so that a custom HTTP client (with the logging transport) can be combined
// with a global RPS limit.
type rateLimitedJSONRPCClient struct {
	rpcClient   jsonrpc.RPCClient
	rateLimiter ratelimit.Limiter
}

var _ rpc.JSONRPCClient = (*rateLimitedJSONRPCClient)(nil)

// NewRateLimitedJSONRPCClient wraps a JSON-RPC client with a global
// requests-per-second limit, equivalent to rpc.NewWithRateLimit.
func NewRateLimitedJSONRPCClient(rpcClient jsonrpc.RPCClient, rps int) rpc.JSONRPCClient {
	return &rateLimitedJSONRPCClient{
		rpcClient:   rpcClient,
		rateLimiter: ratelimit.New(rps),
	}
}

func (c *rateLimitedJSONRPCClient) CallForInto(
	ctx context.Context,
	out interface{},
	method string,
	params []interface{},
) error {
	c.rateLimiter.Take()

	// FIX: Removed '&out'. 'out' is already passed as an interface pointer.
	// Passing '&out' passes a pointer-to-an-interface, which breaks JSON unmarshalling.
	return c.rpcClient.CallForInto(ctx, out, method, params)
}

func (c *rateLimitedJSONRPCClient) CallWithCallback(
	ctx context.Context,
	method string,
	params []interface{},
	callback func(*http.Request, *http.Response) error,
) error {
	c.rateLimiter.Take()

	return c.rpcClient.CallWithCallback(ctx, method, params, callback)
}

func (c *rateLimitedJSONRPCClient) CallBatch(
	ctx context.Context,
	requests jsonrpc.RPCRequests,
) (jsonrpc.RPCResponses, error) {
	c.rateLimiter.Take()

	return c.rpcClient.CallBatch(ctx, requests)
}

func (c *rateLimitedJSONRPCClient) Close() error {
	if closer, ok := c.rpcClient.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}

const (
	// IMPROVEMENT: Tuned timeouts down from 90s/180s to fail-fast, production values.
	// This keeps tracker routines from blocking indefinitely if the node goes dark.
	defaultHTTPTimeout         = 15 * time.Second
	defaultHTTPKeepAlive       = 30 * time.Second
	defaultMaxIdleConnsPerHost = 100
)

// NewRateLimitedRPCClient creates a Solana RPC client equivalent to
// rpc.NewWithCustomRPCClient(rpc.NewWithRateLimit(endpoint, rps)), but with an
// HTTP transport that logs the X-Ratelimit-*/Retry-After response headers
// whenever the node responds with 429. A nil logger falls back to hclog.Default().
//
// When apiKey is non-empty it is appended to the endpoint as the "api_key"
// query parameter (e.g. for OrbitFlare endpoints); pass "" for endpoints that
// do not require a key or that already embed credentials in the URL.
func NewRateLimitedRPCClient(endpoint string, rps int, logger hclog.Logger) *rpc.Client {
	transport := &http.Transport{
		IdleConnTimeout:     defaultHTTPTimeout,
		MaxIdleConns:        defaultMaxIdleConnsPerHost,
		MaxConnsPerHost:     defaultMaxIdleConnsPerHost,
		MaxIdleConnsPerHost: defaultMaxIdleConnsPerHost, // Allows Go to hold open sockets up to your RPS max
		Proxy:               http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   defaultHTTPTimeout,
			KeepAlive: defaultHTTPKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second, // Force-kill un-responsive handshakes early
		DisableKeepAlives:     false,
	}

	httpClient := &http.Client{
		Timeout: defaultHTTPTimeout,
		Transport: &RateLimitLoggingTransport{
			Base:   transport,
			Logger: logger,
		},
	}

	rpcClient := jsonrpc.NewClientWithOpts(endpoint, &jsonrpc.RPCClientOpts{
		HTTPClient: httpClient,
	})

	return rpc.NewWithCustomRPCClient(NewRateLimitedJSONRPCClient(rpcClient, rps))
}
