package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/example/vertex/internal/config"
	"github.com/example/vertex/internal/ethrpc"
	"github.com/example/vertex/internal/indexer"
	"github.com/example/vertex/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
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

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	store := postgres.New(pool)
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	service := indexer.Service{Source: client, Store: store, ChainID: chainID, Start: cfg.StartBlock, BatchSize: cfg.BatchSize}
	indexed, err := service.RunOnce(ctx)
	if err != nil {
		return fmt.Errorf("index range: %w", err)
	}

	slog.Info("block range indexed", "chain_id", chainID, "blocks", indexed)
	return nil
}
