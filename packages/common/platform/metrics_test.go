package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestMetricsMiddlewareAndHandler(t *testing.T) {
	metrics := NewMetrics("test-service")
	handler := MetricsMiddleware(metrics, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/example", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	metricsResponse := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	if !strings.Contains(body, `niceagent_http_requests_total{service="test-service",method="POST",path="/api/example",status="202"} 1`) {
		t.Fatalf("metrics body = %s", body)
	}
}

func TestTraceMiddlewarePropagatesHeaders(t *testing.T) {
	var gotTraceID string
	handler := WithTraceID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID = TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if gotTraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %q", gotTraceID)
	}
	if response.Header().Get("X-Trace-ID") != gotTraceID {
		t.Fatalf("response trace id = %q", response.Header().Get("X-Trace-ID"))
	}
}

func TestInjectTraceHeadersPropagatesOpenTelemetryContext(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))

	header := http.Header{}
	InjectTraceHeaders(ctx, header)

	if got := header.Get("X-Trace-ID"); got != traceID.String() {
		t.Fatalf("X-Trace-ID = %q", got)
	}
	if got := header.Get("Traceparent"); !strings.Contains(got, traceID.String()) {
		t.Fatalf("Traceparent = %q", got)
	}
}

func TestOpenTelemetryConfigFromEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "custom-service")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-api-key=secret,tenant=niceagent")

	cfg := OpenTelemetryConfigFromEnv("fallback-service", "test")
	if cfg.ServiceName != "custom-service" || cfg.Environment != "test" || cfg.Exporter != "otlp" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Endpoint != "http://collector:4318" || !cfg.Insecure {
		t.Fatalf("unexpected endpoint config: %+v", cfg)
	}
	if cfg.Headers["x-api-key"] != "secret" || cfg.Headers["tenant"] != "niceagent" {
		t.Fatalf("unexpected headers: %+v", cfg.Headers)
	}
}

func TestStartSpanReturnsContextAndEndFunc(t *testing.T) {
	ctx, end := StartSpan(context.Background(), "niceagent/test", "unit.operation", Labels{"run_id": "run-1"})
	if ctx == nil || end == nil {
		t.Fatal("expected context and end func")
	}
	end(nil, Labels{"status": "ok"})
}
