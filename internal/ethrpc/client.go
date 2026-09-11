package ethrpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type Client struct {
	url        string
	httpClient *http.Client
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// Block is the canonical block header data persisted by the indexer. Transaction
// bodies are intentionally deferred until the transaction-indexing step.
type Block struct {
	Number     uint64
	Hash       string
	ParentHash string
	Timestamp  uint64
}

type rpcBlock struct {
	Number     string `json:"number"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
	Timestamp  string `json:"timestamp"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(url string, httpClient *http.Client) *Client {
	return &Client{url: url, httpClient: httpClient}
}

func (c *Client) ChainID(ctx context.Context) (uint64, error) {
	return c.hexUint64(ctx, "eth_chainId", nil)
}

func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.hexUint64(ctx, "eth_blockNumber", nil)
}

func (c *Client) BlockByNumber(ctx context.Context, number uint64) (Block, error) {
	var value *rpcBlock
	if err := c.call(ctx, "eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", number), false}, &value); err != nil {
		return Block{}, err
	}
	if value == nil {
		return Block{}, fmt.Errorf("eth_getBlockByNumber returned no block for %d", number)
	}
	returnedNumber, err := parseHexUint64("block number", value.Number)
	if err != nil {
		return Block{}, err
	}
	if returnedNumber != number {
		return Block{}, fmt.Errorf("requested block %d, node returned %d", number, returnedNumber)
	}
	timestamp, err := parseHexUint64("block timestamp", value.Timestamp)
	if err != nil {
		return Block{}, err
	}
	if !validHash(value.Hash) || !validHash(value.ParentHash) {
		return Block{}, fmt.Errorf("block %d returned an invalid hash", number)
	}
	return Block{Number: number, Hash: strings.ToLower(value.Hash), ParentHash: strings.ToLower(value.ParentHash), Timestamp: timestamp}, nil
}

func (c *Client) hexUint64(ctx context.Context, method string, params []any) (uint64, error) {
	var value string
	if err := c.call(ctx, method, params, &value); err != nil {
		return 0, err
	}
	return parseHexUint64(method+" result", value)
}

func parseHexUint64(name, value string) (uint64, error) {
	if !strings.HasPrefix(value, "0x") || len(value) <= 2 {
		return 0, fmt.Errorf("%s is invalid hex quantity %q", name, value)
	}
	n, err := strconv.ParseUint(value[2:], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	return n, nil
}

func validHash(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func (c *Client) call(ctx context.Context, method string, params []any, result any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("call %s: HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(limited)))
	}

	var envelope response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("call %s: RPC error %d: %s", method, envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 {
		return fmt.Errorf("call %s: response has no result", method)
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}
