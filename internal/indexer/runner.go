package indexer

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type retryAfterError interface {
	RetryAfter() time.Duration
}

type Runner struct {
	Service      Service
	PollInterval time.Duration
	RetryInitial time.Duration
	MaxRetries   int
	OnError      func(error)
	OnResult     func(context.Context, uint64, error)
}

func (r Runner) Run(ctx context.Context) error {
	if r.PollInterval <= 0 || r.RetryInitial <= 0 || r.MaxRetries < 0 {
		return fmt.Errorf("invalid runner configuration")
	}
	for {
		indexed, err := r.runWithRetry(ctx)
		if r.OnResult != nil {
			r.OnResult(ctx, indexed, err)
		}
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
	var span trace.Span
	if r.Service.Tracer != nil {
		ctx, span = r.Service.Tracer.Start(ctx, "indexer.run_with_retry")
		defer span.End()
	}
	delay := r.RetryInitial
	var lastErr error
	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		indexed, err := r.Service.RunOnce(ctx)
		if err == nil {
			if span != nil {
				span.SetAttributes(attribute.Int("indexer.attempts", attempt+1))
			}
			return indexed, nil
		}
		lastErr = err
		if attempt == r.MaxRetries {
			break
		}
		wait := retryDelay(lastErr, delay)
		if span != nil {
			span.AddEvent("indexer.retry_scheduled", trace.WithAttributes(
				attribute.Int("retry.attempt", attempt+2),
				attribute.Int64("retry.delay_ms", wait.Milliseconds()),
			))
		}
		timer := time.NewTimer(wait)
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
	err := fmt.Errorf("index range failed after %d attempts: %w", r.MaxRetries+1, lastErr)
	if span != nil {
		span.SetAttributes(attribute.Int("indexer.attempts", r.MaxRetries+1))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return 0, err
}

func retryDelay(err error, fallback time.Duration) time.Duration {
	var limited retryAfterError
	if errors.As(err, &limited) && limited.RetryAfter() > fallback {
		fallback = limited.RetryAfter()
	}
	// Independent workers otherwise retry at the same instant and create a
	// second traffic spike. Add up to 25 percent jitter.
	jitterRange := fallback / 4
	if jitterRange <= 0 {
		return fallback
	}
	return fallback + time.Duration(rand.Int64N(int64(jitterRange)+1))
}
