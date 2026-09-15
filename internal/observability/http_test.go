package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHealthAndReadiness(t *testing.T) {
	readyErr := errors.New("database unavailable")
	handler := Handler(prometheus.NewRegistry(), func(context.Context) error { return readyErr })

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}

	notReady := httptest.NewRecorder()
	handler.ServeHTTP(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", notReady.Code)
	}

	readyErr = nil
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want 200", ready.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry, 1)
	metrics.ObserveChain(20, 16)
	handler := Handler(registry, func(context.Context) error { return nil })

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", response.Code)
	}
	if body := response.Body.String(); !containsAll(body, `vertex_chain_head{chain_id="1"} 20`, `vertex_index_lag_blocks{chain_id="1"} 5`) {
		t.Fatalf("metrics body missing expected values:\n%s", body)
	}
}

func TestTraceObservabilityRequest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/metrics", want: false},
		{path: "/healthz", want: false},
		{path: "/readyz", want: true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if got := traceObservabilityRequest(request); got != test.want {
				t.Fatalf("traceObservabilityRequest(%q) = %t, want %t", test.path, got, test.want)
			}
		})
	}
}

func containsAll(value string, values ...string) bool {
	for _, candidate := range values {
		if !strings.Contains(value, candidate) {
			return false
		}
	}
	return true
}
