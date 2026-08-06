package trackertest

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// The simulated chain is exposed to the tracker over a real httptest JSON-RPC endpoint, so
// the whole client stack under test stays production code: the solana-go client, its JSON
// decoding, and common.MutexRPCClient with its per-method rate limiters. Only the responses
// are simulated, and they are shaped exactly like a validator's.

const (
	// maxSignaturesPerQuery is the validator's cap on getSignaturesForAddress results,
	// and therefore the limit applied when a request does not specify one.
	maxSignaturesPerQuery = 1000

	// Validator error codes the tracker classifies on.
	rpcErrCodeTxNotFound        = -32020
	rpcErrCodeBlockNotAvailable = -32004

	// JSON-RPC protocol error codes.
	rpcErrCodeMethodNotFound = -32601
	rpcErrCodeInvalidParams  = -32602
)

// rpcError is a JSON-RPC level error, reported to the client with its code so error
// classification in the tracker (such as IsCursorNotFoundErr) is exercised for real.
type rpcError struct {
	code    int
	message string
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.code, e.message)
}

func signatureNotFoundError(signature solana.Signature) error {
	return &rpcError{
		code:    rpcErrCodeTxNotFound,
		message: fmt.Sprintf("Transaction signature %s not found", signature),
	}
}

type jsonRPCRequest struct {
	ID      json.RawMessage   `json:"id"`
	JSONRPC string            `json:"jsonrpc"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type jsonRPCErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	ID      json.RawMessage   `json:"id"`
	JSONRPC string            `json:"jsonrpc"`
	Result  json.RawMessage   `json:"result,omitempty"`
	Error   *jsonRPCErrorBody `json:"error,omitempty"`
}

// StartRPCServer serves the simulated chain over JSON-RPC for the duration of the test and
// returns a solana-go client pointed at it.
func (s *ChainSimulator) StartRPCServer() *rpc.Client {
	server := httptest.NewServer(http.HandlerFunc(s.handleRPC))
	s.t.Cleanup(server.Close)

	return rpc.New(server.URL)
}

func (s *ChainSimulator) handleRPC(w http.ResponseWriter, r *http.Request) {
	var request jsonRPCRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	s.mu.Lock()
	s.callCounts[request.Method]++
	s.mu.Unlock()

	if s.cfg.RPCLatency > 0 {
		time.Sleep(s.cfg.RPCLatency)
	}

	result, err := s.dispatch(request)

	response := jsonRPCResponse{ID: request.ID, JSONRPC: "2.0"}

	var simErr *rpcError

	switch {
	case err == nil:
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			http.Error(w, marshalErr.Error(), http.StatusInternalServerError)

			return
		}

		response.Result = encoded
	case errors.As(err, &simErr):
		response.Error = &jsonRPCErrorBody{Code: simErr.code, Message: simErr.message}
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.t.Logf("failed to write rpc response: %s", err)
	}
}

// dispatch routes a request to the simulated chain. Methods the tracker does not call are
// reported as unsupported rather than silently answered.
func (s *ChainSimulator) dispatch(request jsonRPCRequest) (any, error) {
	switch request.Method {
	case "getSlot":
		// The slot the node is on, block or no block in it, which is what a caller
		// checking how far the chain has actually got asks for.
		return s.CurrentHeadSlot(), nil
	case "getBlockHeight":
		return s.getBlockHeight(), nil
	case "getBlocks":
		return s.handleGetBlocks(request)
	case "getBlock":
		return s.handleGetBlock(request)
	case "getSignaturesForAddress":
		return s.handleGetSignaturesForAddress(request)
	case "getTransaction":
		return s.handleGetTransaction(request)
	default:
		return nil, &rpcError{
			code:    rpcErrCodeMethodNotFound,
			message: "method not supported by simulator: " + request.Method,
		}
	}
}

func (s *ChainSimulator) handleGetBlocks(request jsonRPCRequest) (any, error) {
	var startSlot uint64
	if err := unmarshalParam(request, 0, &startSlot); err != nil {
		return nil, err
	}

	var endSlot *uint64

	if len(request.Params) > 1 {
		// The optional end slot is only a number; a commitment object may follow it.
		var candidate uint64
		if err := json.Unmarshal(request.Params[1], &candidate); err == nil {
			endSlot = &candidate
		}
	}

	return s.getBlocks(startSlot, endSlot), nil
}

func (s *ChainSimulator) handleGetBlock(request jsonRPCRequest) (any, error) {
	var slot uint64
	if err := unmarshalParam(request, 0, &slot); err != nil {
		return nil, err
	}

	block := s.BlockAt(slot)
	if block == nil {
		// A skipped slot has no block, which the validator reports as an error.
		return nil, &rpcError{
			code:    rpcErrCodeBlockNotAvailable,
			message: fmt.Sprintf("Block not available for slot %d", slot),
		}
	}

	blockTime := solana.UnixTimeSeconds(block.blockTime.Unix())
	height := block.Height

	return rpc.GetBlockResult{
		Blockhash:         block.Hash,
		PreviousBlockhash: block.parentHash,
		ParentSlot:        block.parentSlot,
		Signatures:        block.signatures,
		BlockTime:         &blockTime,
		BlockHeight:       &height,
	}, nil
}

func (s *ChainSimulator) handleGetSignaturesForAddress(request jsonRPCRequest) (any, error) {
	var address solana.PublicKey
	if err := unmarshalParam(request, 0, &address); err != nil {
		return nil, err
	}

	opts := struct {
		Before solana.Signature `json:"before"`
		Until  solana.Signature `json:"until"`
		Limit  *int             `json:"limit"`
	}{}

	if len(request.Params) > 1 {
		if err := json.Unmarshal(request.Params[1], &opts); err != nil {
			return nil, err
		}
	}

	limit := maxSignaturesPerQuery
	if opts.Limit != nil {
		limit = *opts.Limit
	}

	query := SignatureQuery{
		Before: opts.Before,
		Until:  opts.Until,
		Limit:  limit,
	}

	signatures, err := s.getSignaturesForAddress(address, opts.Before, opts.Until, limit)
	if err != nil {
		var simErr *rpcError
		if errors.As(err, &simErr) {
			query.ErrorCode = simErr.code
			s.recordSignatureQuery(query)
		}

		return nil, err
	}

	query.Returned = len(signatures)

	if len(signatures) > 0 {
		query.OldestReturned = signatures[len(signatures)-1].Signature
	}

	s.recordSignatureQuery(query)

	return signatures, nil
}

func (s *ChainSimulator) handleGetTransaction(request jsonRPCRequest) (any, error) {
	var signature solana.Signature
	if err := unmarshalParam(request, 0, &signature); err != nil {
		return nil, err
	}

	tx := s.getTransaction(signature)
	if tx == nil {
		return nil, nil // a validator returns null for an unknown signature
	}

	return map[string]any{
		"slot":      tx.Slot,
		"blockTime": tx.ProducedAt.Unix(),
		"meta": map[string]any{
			"err":               nil,
			"fee":               5000,
			"preBalances":       []uint64{1_000_000_000, 0},
			"postBalances":      []uint64{999_995_000, 0},
			"innerInstructions": []any{},
			"logMessages":       tx.logs,
		},
		// base64 encoding, the same shape the validator returns for encoding=base64.
		"transaction": []any{base64.StdEncoding.EncodeToString(tx.raw), "base64"},
	}, nil
}

func unmarshalParam(request jsonRPCRequest, index int, out any) error {
	if len(request.Params) <= index {
		return &rpcError{
			code:    rpcErrCodeInvalidParams,
			message: fmt.Sprintf("%s: missing param at index %d", request.Method, index),
		}
	}

	return json.Unmarshal(request.Params[index], out)
}
