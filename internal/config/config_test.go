package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Setenv("RPC_URL", "http://node.example")
	t.Setenv("DATABASE_URL", "postgres://indexer@localhost/vertex")
	t.Setenv("EXPECTED_CHAIN_ID", "8453")
	t.Setenv("RPC_TIMEOUT", "3s")
	t.Setenv("CONFIRMATION_DEPTH", "64")
	t.Setenv("POLL_INTERVAL", "5s")
	t.Setenv("RPC_CONCURRENCY", "16")
	t.Setenv("MAX_RETRIES", "6")
	t.Setenv("RETRY_INITIAL_DELAY", "250ms")

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
	if cfg.ConfirmationDepth != 64 {
		t.Fatalf("ConfirmationDepth = %d, want 64", cfg.ConfirmationDepth)
	}
	if cfg.PollInterval != 5*time.Second || cfg.RPCConcurrency != 16 || cfg.MaxRetries != 6 || cfg.RetryInitial != 250*time.Millisecond {
		t.Fatalf("operational configuration = %+v", cfg)
	}
}

func TestLoadRejectsInvalidConfirmationDepth(t *testing.T) {
	t.Setenv("RPC_URL", "http://node.example")
	t.Setenv("DATABASE_URL", "postgres://indexer@localhost/vertex")
	t.Setenv("CONFIRMATION_DEPTH", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestLoadRequiresRPCURL(t *testing.T) {
	t.Setenv("RPC_URL", "")
	t.Setenv("DATABASE_URL", "postgres://indexer@localhost/vertex")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("RPC_URL", "http://node.example")
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}
