package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultChainID = uint64(1)
	defaultTimeout = 10 * time.Second
)

type Config struct {
	RPCURL          string
	ExpectedChainID uint64
	RPCTimeout      time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		RPCURL:          os.Getenv("RPC_URL"),
		ExpectedChainID: defaultChainID,
		RPCTimeout:      defaultTimeout,
	}

	if cfg.RPCURL == "" {
		return Config{}, fmt.Errorf("RPC_URL is required")
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

	return cfg, nil
}
