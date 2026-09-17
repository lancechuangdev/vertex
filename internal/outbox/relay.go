package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Message struct {
	ID               int64
	ChainID          uint64
	EventType        string
	DeduplicationKey string
	Payload          json.RawMessage
	Traceparent      string
	Tracestate       string
	Attempts         int
}

type Store interface {
	ClaimOutbox(context.Context, uint64, string, int, time.Duration) ([]Message, error)
	MarkOutboxPublished(context.Context, int64, string) error
	ReleaseOutbox(context.Context, int64, string, string, time.Time) error
}

type Publisher interface {
	Publish(context.Context, Message) error
}

type Relay struct {
	Store        Store
	Publisher    Publisher
	Tracer       trace.Tracer
	ChainID      uint64
	WorkerID     string
	BatchSize    int
	PollInterval time.Duration
	Lease        time.Duration
	RetryInitial time.Duration
	OnError      func(error)
}

func (r Relay) Run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	for {
		count, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil && r.OnError != nil {
			r.OnError(err)
		}
		if ctx.Err() != nil {
			return nil
		}
		if count == r.BatchSize {
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

func (r Relay) RunOnce(ctx context.Context) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	messages, err := r.Store.ClaimOutbox(ctx, r.ChainID, r.WorkerID, r.BatchSize, r.Lease)
	if err != nil {
		return 0, fmt.Errorf("claim outbox messages: %w", err)
	}
	var failures []error
	for _, message := range messages {
		publishCtx := ctx
		var span trace.Span
		if r.Tracer != nil {
			options := []trace.SpanStartOption{trace.WithAttributes(
				attribute.Int64("messaging.message.id", message.ID),
				attribute.String("messaging.operation.name", "publish"),
				attribute.String("messaging.destination.name", message.EventType),
				attribute.Int("messaging.message.delivery_count", message.Attempts),
			)}
			if link, ok := producerLink(message); ok {
				options = append(options, trace.WithLinks(link))
			}
			publishCtx, span = r.Tracer.Start(ctx, "outbox.publish", options...)
		}
		err := r.Publisher.Publish(publishCtx, message)
		if err == nil {
			err = r.Store.MarkOutboxPublished(publishCtx, message.ID, r.WorkerID)
		}
		if err != nil {
			if span != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			retryAt := time.Now().Add(retryDelay(r.RetryInitial, message.Attempts))
			if releaseErr := r.Store.ReleaseOutbox(ctx, message.ID, r.WorkerID, err.Error(), retryAt); releaseErr != nil {
				err = errors.Join(err, fmt.Errorf("release outbox message: %w", releaseErr))
			}
			failures = append(failures, fmt.Errorf("publish outbox message %d: %w", message.ID, err))
		}
		if span != nil {
			span.End()
		}
	}
	return len(messages), errors.Join(failures...)
}

func (r Relay) validate() error {
	if r.Store == nil || r.Publisher == nil || r.ChainID == 0 || r.WorkerID == "" || r.BatchSize < 1 || r.PollInterval <= 0 || r.Lease <= 0 || r.RetryInitial <= 0 {
		return fmt.Errorf("invalid outbox relay configuration")
	}
	return nil
}

func producerLink(message Message) (trace.Link, bool) {
	carrier := propagation.MapCarrier{}
	if message.Traceparent != "" {
		carrier.Set("traceparent", message.Traceparent)
	}
	if message.Tracestate != "" {
		carrier.Set("tracestate", message.Tracestate)
	}
	linked := propagation.TraceContext{}.Extract(context.Background(), carrier)
	spanContext := trace.SpanContextFromContext(linked)
	return trace.Link{SpanContext: spanContext}, spanContext.IsValid()
}

func retryDelay(initial time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	for i := 1; i < attempt && initial < time.Hour; i++ {
		initial *= 2
	}
	if initial > time.Hour {
		return time.Hour
	}
	return initial
}

// LogPublisher is a runnable demo transport. A production Publisher can send
// the same message to Kafka, SQS, or another broker without changing the relay.
type LogPublisher struct{}

func (LogPublisher) Publish(ctx context.Context, message Message) error {
	spanContext := trace.SpanContextFromContext(ctx)
	slog.Info("outbox message published",
		"message_id", message.ID,
		"chain_id", message.ChainID,
		"event_type", message.EventType,
		"deduplication_key", message.DeduplicationKey,
		"payload", string(message.Payload),
		"trace_id", spanContext.TraceID().String(),
		"span_id", spanContext.SpanID().String(),
	)
	return nil
}
