package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/example/vertex/internal/config"
	"github.com/example/vertex/internal/ethrpc"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("indexer startup failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	client := ethrpc.New(cfg.RPCURL, &http.Client{Timeout: cfg.RPCTimeout})
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("verify chain: %w", err)
	}
	if chainID != cfg.ExpectedChainID {
		return fmt.Errorf("unexpected chain ID: node returned %d, expected %d", chainID, cfg.ExpectedChainID)
	}

	latestBlock, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("read latest block: %w", err)
	}

	slog.Info("chain connection verified", "chain_id", chainID, "latest_block", latestBlock)
	return nil
}
