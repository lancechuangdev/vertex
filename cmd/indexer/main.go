package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/example/vertex/internal/config"
	"github.com/example/vertex/internal/ethrpc"
	"github.com/example/vertex/internal/indexer"
	"github.com/example/vertex/internal/observability"
	"github.com/example/vertex/internal/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("indexer startup failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	tracer, shutdownTracing, err := observability.SetupTracing(ctx, "vertex-indexer")
	if err != nil {
		return fmt.Errorf("configure tracing: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			slog.Error("failed to flush traces", "error", err)
		}
	}()
	registry := prometheus.NewRegistry()
	metrics := observability.NewMetrics(registry)
	rawClient := ethrpc.New(cfg.RPCURL, &http.Client{Timeout: cfg.RPCTimeout})
	client := observability.Source{Next: rawClient, Metrics: metrics, Tracer: tracer}
	chainID, err := rawClient.ChainID(ctx)
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
	release, acquired, err := store.AcquireChainLock(ctx, chainID)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("another indexer holds the worker lock for chain %d", chainID)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := release(releaseCtx); err != nil {
			slog.Error("failed to release chain worker lock", "error", err)
		}
	}()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	service := indexer.Service{
		Source: client, Store: store, ChainID: chainID, Start: cfg.StartBlock,
		BatchSize: cfg.BatchSize, ConfirmationDepth: cfg.ConfirmationDepth,
		Concurrency: cfg.RPCConcurrency,
		Observer:    metrics, Tracer: tracer,
	}
	mode := "run"
	if len(args) > 0 {
		mode = args[0]
	}
	switch mode {
	case "once":
		if len(args) != 1 {
			return fmt.Errorf("usage: indexer once")
		}
		indexed, err := service.RunOnce(ctx)
		if err != nil {
			return fmt.Errorf("index range: %w", err)
		}
		slog.Info("block range indexed", "chain_id", chainID, "blocks", indexed)
		return nil
	case "replay-and-run":
		if len(args) != 2 {
			return fmt.Errorf("usage: indexer replay-and-run BLOCK_NUMBER")
		}
		from, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("replay block must be a non-negative decimal integer")
		}
		if err := service.ReplayFrom(ctx, from); err != nil {
			return err
		}
		slog.Info("checkpoint rewound for replay", "chain_id", chainID, "block", from)
	case "run":
		if len(args) > 1 {
			return fmt.Errorf("usage: indexer [run|once|replay-and-run BLOCK_NUMBER]")
		}
	default:
		return fmt.Errorf("unknown command %q; use run, once, or replay-and-run BLOCK_NUMBER", mode)
	}

	runner := indexer.Runner{
		Service: service, PollInterval: cfg.PollInterval,
		RetryInitial: cfg.RetryInitial, MaxRetries: cfg.MaxRetries,
		OnError: func(err error) { slog.Error("indexing attempt failed", "chain_id", chainID, "error", err) },
		OnResult: func(resultCtx context.Context, blocks uint64, runErr error) {
			metrics.ObserveRun(runErr)
			outbox, deadLetters, err := store.Backlogs(resultCtx, chainID)
			if err != nil {
				slog.Error("failed to collect backlog metrics", "chain_id", chainID, "error", err)
				return
			}
			metrics.SetBacklogs(outbox, deadLetters)
			slog.Info("indexing cycle completed", "chain_id", chainID, "blocks", blocks, "outbox_backlog", outbox, "dead_letter_backlog", deadLetters, "error", runErr)
		},
	}
	server := &http.Server{
		Addr:              cfg.ObservabilityAddr,
		Handler:           observability.Handler(registry, store.Ping),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("observability server listening", "address", cfg.ObservabilityAddr)
		serverErrors <- server.ListenAndServe()
	}()
	runnerErrors := make(chan error, 1)
	go func() { runnerErrors <- runner.Run(ctx) }()

	slog.Info("indexer running", "chain_id", chainID)
	var runErr error
	runnerFinished := false
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("observability server: %w", err)
		}
		cancel()
	case err := <-runnerErrors:
		runErr = err
		runnerFinished = true
		cancel()
	case <-ctx.Done():
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown observability server: %w", err)
	}
	if !runnerFinished {
		select {
		case err := <-runnerErrors:
			if err != nil && runErr == nil {
				runErr = err
			}
		case <-shutdownCtx.Done():
			if runErr == nil {
				runErr = fmt.Errorf("indexer did not stop before shutdown deadline")
			}
		}
	}
	return runErr
}
