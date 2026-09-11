package ethrpc

import (
	"context"
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
