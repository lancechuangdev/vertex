package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("RPC_URL", "http://node.example")
	t.Setenv("EXPECTED_CHAIN_ID", "8453")
	t.Setenv("RPC_TIMEOUT", "3s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ExpectedChainID != 8453 {
		t.Fatalf("ExpectedChainID = %d, want 8453", cfg.ExpectedChainID)
	}
	if cfg.RPCTimeout.String() != "3s" {
		t.Fatalf("RPCTimeout = %s, want 3s", cfg.RPCTimeout)
	}
}

func TestLoadRequiresRPCURL(t *testing.T) {
	t.Setenv("RPC_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}
