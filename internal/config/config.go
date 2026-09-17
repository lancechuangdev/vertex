package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

const (
	defaultChainID            = uint64(1)
	defaultTimeout            = 10 * time.Second
	defaultBatchSize          = uint64(100)
	defaultConfirmationDepth  = uint64(12)
	defaultPollInterval       = 2 * time.Second
	defaultRPCConcurrency     = 8
	defaultRPCRateLimit       = 5.0
	defaultMaxRetries         = 4
	defaultRetryInitial       = 500 * time.Millisecond
	defaultObservabilityAddr  = ":9090"
	defaultOutboxBatchSize    = 100
	defaultOutboxPollInterval = time.Second
	defaultOutboxLease        = 30 * time.Second
	defaultOutboxRetryInitial = time.Second
	maxBatchSize              = uint64(1000)
	maxRPCConcurrency         = 128
	maxRetries                = 20
)

type Config struct {
	RPCURL             string
	DatabaseURL        string
	ExpectedChainID    uint64
	RPCTimeout         time.Duration
	StartBlock         uint64
	BatchSize          uint64
	ConfirmationDepth  uint64
	PollInterval       time.Duration
	RPCConcurrency     int
	RPCRateLimit       float64
	MaxRetries         int
	RetryInitial       time.Duration
	ObservabilityAddr  string
	OutboxBatchSize    int
	OutboxPollInterval time.Duration
	OutboxLease        time.Duration
	OutboxRetryInitial time.Duration
}

func Load() (Config, error) {
	databaseURL, err := loadDatabaseURL()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		RPCURL:             os.Getenv("RPC_URL"),
		DatabaseURL:        databaseURL,
		ExpectedChainID:    defaultChainID,
		RPCTimeout:         defaultTimeout,
		BatchSize:          defaultBatchSize,
		ConfirmationDepth:  defaultConfirmationDepth,
		PollInterval:       defaultPollInterval,
		RPCConcurrency:     defaultRPCConcurrency,
		RPCRateLimit:       defaultRPCRateLimit,
		MaxRetries:         defaultMaxRetries,
		RetryInitial:       defaultRetryInitial,
		ObservabilityAddr:  defaultObservabilityAddr,
		OutboxBatchSize:    defaultOutboxBatchSize,
		OutboxPollInterval: defaultOutboxPollInterval,
		OutboxLease:        defaultOutboxLease,
		OutboxRetryInitial: defaultOutboxRetryInitial,
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

	if value := os.Getenv("RPC_RATE_LIMIT"); value != "" {
		rateLimit, err := strconv.ParseFloat(value, 64)
		if err != nil || rateLimit <= 0 || rateLimit > 10000 {
			return Config{}, fmt.Errorf("RPC_RATE_LIMIT must be greater than 0 and at most 10000")
		}
		cfg.RPCRateLimit = rateLimit
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

	if value := os.Getenv("OBSERVABILITY_ADDR"); value != "" {
		cfg.ObservabilityAddr = value
	}
	if value := os.Getenv("OUTBOX_BATCH_SIZE"); value != "" {
		size, err := strconv.Atoi(value)
		if err != nil || size < 1 || size > 1000 {
			return Config{}, fmt.Errorf("OUTBOX_BATCH_SIZE must be between 1 and 1000")
		}
		cfg.OutboxBatchSize = size
	}
	for name, target := range map[string]*time.Duration{
		"OUTBOX_POLL_INTERVAL":       &cfg.OutboxPollInterval,
		"OUTBOX_LEASE":               &cfg.OutboxLease,
		"OUTBOX_RETRY_INITIAL_DELAY": &cfg.OutboxRetryInitial,
	} {
		if value := os.Getenv(name); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil || duration <= 0 {
				return Config{}, fmt.Errorf("%s must be a positive duration", name)
			}
			*target = duration
		}
	}

	return cfg, nil
}

// loadDatabaseURL accepts a complete URL for local development, or assembles
// one from individual fields so ECS can inject only the password from Secrets
// Manager without materializing the complete credential in Terraform state.
func loadDatabaseURL() (string, error) {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value, nil
	}
	host := os.Getenv("DATABASE_HOST")
	password := os.Getenv("DATABASE_PASSWORD")
	if host == "" && password == "" {
		return "", nil
	}
	if host == "" || password == "" {
		return "", fmt.Errorf("DATABASE_HOST and DATABASE_PASSWORD must both be set when DATABASE_URL is absent")
	}
	user := envOrDefault("DATABASE_USER", "vertex")
	port := envOrDefault("DATABASE_PORT", "5432")
	name := envOrDefault("DATABASE_NAME", "vertex")
	sslmode := envOrDefault("DATABASE_SSLMODE", "require")
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     host + ":" + port,
		Path:     "/" + name,
		RawQuery: url.Values{"sslmode": []string{sslmode}}.Encode(),
	}
	return u.String(), nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
