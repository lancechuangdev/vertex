package ethrpc

import (
	"bytes"
	"context"
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
	return c.hexUint64(ctx, "eth_chainId")
}

func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.hexUint64(ctx, "eth_blockNumber")
}

func (c *Client) hexUint64(ctx context.Context, method string) (uint64, error) {
	var value string
	if err := c.call(ctx, method, &value); err != nil {
		return 0, err
	}
	if !strings.HasPrefix(value, "0x") || len(value) <= 2 {
		return 0, fmt.Errorf("%s returned invalid hex quantity %q", method, value)
	}
	n, err := strconv.ParseUint(value[2:], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s result %q: %w", method, value, err)
	}
	return n, nil
}

func (c *Client) call(ctx context.Context, method string, result any) error {
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: 1, Method: method, Params: []any{}})
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
