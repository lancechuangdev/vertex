package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestSetupTracingConfiguresW3CPropagation(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	_, _, err := SetupTracing(context.Background(), "test-service", 1)
	if err != nil {
		t.Fatalf("SetupTracing() error = %v", err)
	}

	traceState, err := trace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatalf("ParseTraceState() error = %v", err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{2},
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
	})
	member, err := baggage.NewMember("tenant", "acme")
	if err != nil {
		t.Fatalf("NewMember() error = %v", err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatalf("New() baggage error = %v", err)
	}
	ctx := baggage.ContextWithBaggage(trace.ContextWithSpanContext(context.Background(), spanContext), bag)
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	if carrier.Get("traceparent") == "" || carrier.Get("tracestate") != "vendor=value" || carrier.Get("baggage") != "tenant=acme" {
		t.Fatalf("injected propagation headers = %v", carrier)
	}
	extracted := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	if got := trace.SpanContextFromContext(extracted); got.TraceID() != spanContext.TraceID() || got.SpanID() != spanContext.SpanID() {
		t.Fatalf("extracted span context = %v, want %v", got, spanContext)
	}
}
