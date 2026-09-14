package ethrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPRateLimitPreservesRetryAfter(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"7"}},
			Body:       io.NopCloser(strings.NewReader(`{"code":-32005,"message":"Too Many Requests"}`)),
		}, nil
	})}
	_, err := New("http://node.example", httpClient).BlockNumber(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("BlockNumber() error = %v, want HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests || httpErr.RetryAfter() != 7*time.Second {
		t.Fatalf("HTTPError = %+v", httpErr)
	}
}

func TestRateLimiterHonorsCancellation(t *testing.T) {
	client := New("http://node.example", testHTTPClient(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`), WithRateLimit(1))
	if _, err := client.BlockNumber(context.Background()); err != nil {
		t.Fatalf("first BlockNumber() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.BlockNumber(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("second BlockNumber() error = %v, want context cancellation", err)
	}
}

func testHTTPClient(body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
}

func TestChainID(t *testing.T) {
	client := New("http://node.example", testHTTPClient(`{"jsonrpc":"2.0","id":1,"result":"0x2105"}`))
	got, err := client.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID() error = %v", err)
	}
	if got != 8453 {
		t.Fatalf("ChainID() = %d, want 8453", got)
	}
}

func TestRPCError(t *testing.T) {
	client := New("http://node.example", testHTTPClient(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"not found"}}`))
	if _, err := client.BlockNumber(context.Background()); err == nil {
		t.Fatal("BlockNumber() error = nil, want an error")
	}
}

func TestBlockByNumber(t *testing.T) {
	const hash = "0x1111111111111111111111111111111111111111111111111111111111111111"
	const parent = "0x2222222222222222222222222222222222222222222222222222222222222222"
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var got request
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Method != "eth_getBlockByNumber" || len(got.Params) != 2 || got.Params[0] != "0x2a" || got.Params[1] != true {
			t.Fatalf("unexpected RPC request: %+v", got)
		}
		body := `{"jsonrpc":"2.0","id":1,"result":{"number":"0x2a","hash":"` + hash + `","parentHash":"` + parent + `","timestamp":"0x64"}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}

	block, err := New("http://node.example", httpClient).BlockByNumber(context.Background(), 42)
	if err != nil {
		t.Fatalf("BlockByNumber() error = %v", err)
	}
	if block.Number != 42 || block.Timestamp != 100 || block.Hash != hash || block.ParentHash != parent {
		t.Fatalf("BlockByNumber() = %+v", block)
	}
}

func TestBlockByNumberDecodesTransactions(t *testing.T) {
	const hash = "0x1111111111111111111111111111111111111111111111111111111111111111"
	const parent = "0x2222222222222222222222222222222222222222222222222222222222222222"
	const txHash = "0x3333333333333333333333333333333333333333333333333333333333333333"
	body := `{"jsonrpc":"2.0","id":1,"result":{"number":"0x1","hash":"` + hash + `","parentHash":"` + parent + `","timestamp":"0x64","transactions":[{"hash":"` + txHash + `","transactionIndex":"0x0","from":"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","to":null,"value":"0xde0b6b3a7640000"}]}}`
	block, err := New("http://node.example", testHTTPClient(body)).BlockByNumber(context.Background(), 1)
	if err != nil {
		t.Fatalf("BlockByNumber() error = %v", err)
	}
	if len(block.Transactions) != 1 || block.Transactions[0].From != "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ||
		block.Transactions[0].To != nil || block.Transactions[0].Value != "1000000000000000000" {
		t.Fatalf("transactions = %+v", block.Transactions)
	}
}

func TestTransactionReceipt(t *testing.T) {
	const txHash = "0x3333333333333333333333333333333333333333333333333333333333333333"
	const topic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	body := `{"jsonrpc":"2.0","id":1,"result":{"transactionHash":"` + txHash + `","blockNumber":"0x2a","status":"0x1","gasUsed":"0x5208","contractAddress":null,"logs":[{"logIndex":"0x0","address":"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","topics":["` + topic + `"],"data":"0x"}]}}`
	receipt, err := New("http://node.example", testHTTPClient(body)).TransactionReceipt(context.Background(), txHash)
	if err != nil {
		t.Fatalf("TransactionReceipt() error = %v", err)
	}
	if receipt.BlockNumber != 42 || receipt.Status == nil || *receipt.Status != 1 || receipt.GasUsed != 21000 || len(receipt.Logs) != 1 {
		t.Fatalf("TransactionReceipt() = %+v", receipt)
	}
	if receipt.Logs[0].Address != "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("log address = %s", receipt.Logs[0].Address)
	}
}
