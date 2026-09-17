package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakeStore struct {
	messages []Message
	acked    []int64
	released []int64
}

func (s *fakeStore) ClaimOutbox(context.Context, uint64, string, int, time.Duration) ([]Message, error) {
	return s.messages, nil
}
func (s *fakeStore) MarkOutboxPublished(_ context.Context, id int64, _ string) error {
	s.acked = append(s.acked, id)
	return nil
}
func (s *fakeStore) ReleaseOutbox(_ context.Context, id int64, _, _ string, _ time.Time) error {
	s.released = append(s.released, id)
	return nil
}

type fakePublisher struct{ err error }

func (p fakePublisher) Publish(context.Context, Message) error { return p.err }

func TestRelayPublishesWithProducerTraceLink(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	producerCtx, producer := provider.Tracer("test").Start(context.Background(), "producer")
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(producerCtx, carrier)
	producer.End()

	store := &fakeStore{messages: []Message{{ID: 7, ChainID: 1, EventType: "token_transfer.confirmed", Attempts: 1, Traceparent: carrier.Get("traceparent"), Tracestate: carrier.Get("tracestate")}}}
	relay := testRelay(store, fakePublisher{}, provider)
	count, err := relay.RunOnce(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("RunOnce() = (%d, %v), want (1, nil)", count, err)
	}
	if len(store.acked) != 1 || store.acked[0] != 7 || len(store.released) != 0 {
		t.Fatalf("acked = %v, released = %v", store.acked, store.released)
	}
	spans := recorder.Ended()
	publish := spans[len(spans)-1]
	if len(publish.Links()) != 1 || publish.Links()[0].SpanContext.TraceID() != producer.SpanContext().TraceID() || publish.Links()[0].SpanContext.SpanID() != producer.SpanContext().SpanID() {
		t.Fatalf("publish links = %v, want producer span context", publish.Links())
	}
}

func TestRelayReleasesFailedPublish(t *testing.T) {
	store := &fakeStore{messages: []Message{{ID: 9, ChainID: 1, Attempts: 1}}}
	relay := testRelay(store, fakePublisher{err: errors.New("broker unavailable")}, nil)
	count, err := relay.RunOnce(context.Background())
	if count != 1 || err == nil {
		t.Fatalf("RunOnce() = (%d, %v), want one claimed message and error", count, err)
	}
	if len(store.acked) != 0 || len(store.released) != 1 || store.released[0] != 9 {
		t.Fatalf("acked = %v, released = %v", store.acked, store.released)
	}
}

func testRelay(store Store, publisher Publisher, provider *sdktrace.TracerProvider) Relay {
	tracer := sdktrace.NewTracerProvider().Tracer("test")
	if provider != nil {
		tracer = provider.Tracer("test")
	}
	return Relay{Store: store, Publisher: publisher, Tracer: tracer, ChainID: 1, WorkerID: "worker", BatchSize: 10, PollInterval: time.Second, Lease: 30 * time.Second, RetryInitial: time.Second}
}
