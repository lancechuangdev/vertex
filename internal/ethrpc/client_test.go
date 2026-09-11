package ethrpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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
		if got.Method != "eth_getBlockByNumber" || len(got.Params) != 2 || got.Params[0] != "0x2a" || got.Params[1] != false {
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
