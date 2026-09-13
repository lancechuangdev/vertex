package indexer

import (
	"context"
	"fmt"
	"time"
)

type Runner struct {
	Service      Service
	PollInterval time.Duration
	RetryInitial time.Duration
	MaxRetries   int
	OnError      func(error)
}

func (r Runner) Run(ctx context.Context) error {
	if r.PollInterval <= 0 || r.RetryInitial <= 0 || r.MaxRetries < 0 {
		return fmt.Errorf("invalid runner configuration")
	}
	for {
		indexed, err := r.runWithRetry(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if r.OnError != nil {
				r.OnError(err)
			}
		}
		if indexed > 0 && err == nil {
			continue
		}
		timer := time.NewTimer(r.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (r Runner) runWithRetry(ctx context.Context) (uint64, error) {
	delay := r.RetryInitial
	var lastErr error
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		indexed, err := r.Service.RunOnce(ctx)
		if err == nil {
			return indexed, nil
		}
		lastErr = err
		if attempt == r.MaxRetries {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
		if delay <= (1<<62)/2 {
			delay *= 2
		}
	}
	return 0, fmt.Errorf("index range failed after %d attempts: %w", r.MaxRetries+1, lastErr)
}
