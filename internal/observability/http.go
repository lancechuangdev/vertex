package observability

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func Handler(reg prometheus.Gatherer, ready func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(r.Context()); err != nil {
			writeStatus(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		writeStatus(w, http.StatusOK, "ready")
	})
	return otelhttp.NewHandler(mux, "observability.http", otelhttp.WithFilter(traceObservabilityRequest))
}

func traceObservabilityRequest(r *http.Request) bool {
	return r.URL.Path != "/metrics" && r.URL.Path != "/healthz"
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
