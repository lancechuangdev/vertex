package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultChainID           = uint64(1)
	defaultTimeout           = 10 * time.Second
	defaultBatchSize         = uint64(100)
	defaultConfirmationDepth = uint64(12)
	defaultPollInterval      = 2 * time.Second
	defaultRPCConcurrency    = 8
	defaultMaxRetries        = 4
	defaultRetryInitial      = 500 * time.Millisecond
	maxBatchSize             = uint64(1000)
	maxRPCConcurrency        = 128
	maxRetries               = 20
)

type Config struct {
	RPCURL            string
	DatabaseURL       string
	ExpectedChainID   uint64
	RPCTimeout        time.Duration
	StartBlock        uint64
	BatchSize         uint64
	ConfirmationDepth uint64
	PollInterval      time.Duration
	RPCConcurrency    int
	MaxRetries        int
	RetryInitial      time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		RPCURL:            os.Getenv("RPC_URL"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		ExpectedChainID:   defaultChainID,
		RPCTimeout:        defaultTimeout,
		BatchSize:         defaultBatchSize,
		ConfirmationDepth: defaultConfirmationDepth,
		PollInterval:      defaultPollInterval,
		RPCConcurrency:    defaultRPCConcurrency,
		MaxRetries:        defaultMaxRetries,
		RetryInitial:      defaultRetryInitial,
	}

	if cfg.RPCURL == "" {
		return Config{}, fmt.Errorf("RPC_URL is required")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	if value := os.Getenv("EXPECTED_CHAIN_ID"); value != "" {
		chainID, err := strconv.ParseUint(value, 10, 64)
		if err != nil || chainID == 0 {
			return Config{}, fmt.Errorf("EXPECTED_CHAIN_ID must be a positive decimal integer")
		}
		cfg.ExpectedChainID = chainID
	}

	if value := os.Getenv("RPC_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("RPC_TIMEOUT must be a positive duration")
		}
		cfg.RPCTimeout = timeout
	}

	if value := os.Getenv("START_BLOCK"); value != "" {
		start, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("START_BLOCK must be a non-negative decimal integer")
		}
		cfg.StartBlock = start
	}

	if value := os.Getenv("BLOCK_BATCH_SIZE"); value != "" {
		batchSize, err := strconv.ParseUint(value, 10, 64)
		if err != nil || batchSize == 0 || batchSize > maxBatchSize {
			return Config{}, fmt.Errorf("BLOCK_BATCH_SIZE must be between 1 and %d", maxBatchSize)
		}
		cfg.BatchSize = batchSize
	}

	if value := os.Getenv("CONFIRMATION_DEPTH"); value != "" {
		depth, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("CONFIRMATION_DEPTH must be a non-negative decimal integer")
		}
		cfg.ConfirmationDepth = depth
	}

	if value := os.Getenv("POLL_INTERVAL"); value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			return Config{}, fmt.Errorf("POLL_INTERVAL must be a positive duration")
		}
		cfg.PollInterval = interval
	}

	if value := os.Getenv("RPC_CONCURRENCY"); value != "" {
		concurrency, err := strconv.Atoi(value)
		if err != nil || concurrency < 1 || concurrency > maxRPCConcurrency {
			return Config{}, fmt.Errorf("RPC_CONCURRENCY must be between 1 and %d", maxRPCConcurrency)
		}
		cfg.RPCConcurrency = concurrency
	}

	if value := os.Getenv("MAX_RETRIES"); value != "" {
		retries, err := strconv.Atoi(value)
		if err != nil || retries < 0 || retries > maxRetries {
			return Config{}, fmt.Errorf("MAX_RETRIES must be between 0 and %d", maxRetries)
		}
		cfg.MaxRetries = retries
	}

	if value := os.Getenv("RETRY_INITIAL_DELAY"); value != "" {
		delay, err := time.ParseDuration(value)
		if err != nil || delay <= 0 {
			return Config{}, fmt.Errorf("RETRY_INITIAL_DELAY must be a positive duration")
		}
		cfg.RetryInitial = delay
	}

	return cfg, nil
}
