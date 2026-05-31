package platform

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type traceIDContextKey struct{}

func WithTraceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := incomingTraceID(r)
		if traceID == "" {
			traceID = OpenTelemetryTraceIDFromContext(r.Context())
		}
		if traceID == "" {
			traceID = NewID("trc")
		}
		w.Header().Set("X-Trace-ID", traceID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), traceIDContextKey{}, traceID)))
	})
}

func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	traceID, _ := ctx.Value(traceIDContextKey{}).(string)
	return traceID
}

func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validTraceHeaderValue(traceID) {
		return ctx
	}
	return context.WithValue(ctx, traceIDContextKey{}, traceID)
}

func InjectTraceHeaders(ctx context.Context, header http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = OpenTelemetryTraceIDFromContext(ctx)
	}
	if traceID == "" {
		return
	}
	header.Set("X-Trace-ID", traceID)
}

func OpenTelemetryTraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.HasTraceID() {
		return ""
	}
	traceID := spanContext.TraceID().String()
	if traceID == "00000000000000000000000000000000" {
		return ""
	}
	return traceID
}

func incomingTraceID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Trace-ID")); validTraceHeaderValue(value) {
		return value
	}
	if traceID := traceIDFromTraceparent(r.Header.Get("Traceparent")); traceID != "" {
		return traceID
	}
	return ""
}

func traceIDFromTraceparent(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) < 4 {
		return ""
	}
	traceID := strings.ToLower(parts[1])
	if len(traceID) != 32 {
		return ""
	}
	for _, ch := range traceID {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return ""
		}
	}
	if traceID == "00000000000000000000000000000000" {
		return ""
	}
	return traceID
}

func validTraceHeaderValue(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}
