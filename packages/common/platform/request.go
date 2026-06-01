package platform

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

type requestIDContextKey struct{}

func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !validTraceHeaderValue(requestID) {
			requestID = NewID("req")
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(ContextWithRequestID(r.Context(), requestID)))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validTraceHeaderValue(requestID) {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func RequestLogger(log *slog.Logger, service string, next http.Handler) http.Handler {
	if log == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &metricResponseWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Info("http_request",
			"service", service,
			"request_id", RequestIDFromContext(r.Context()),
			"trace_id", TraceIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(start).String(),
		)
	})
}
