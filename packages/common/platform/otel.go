package platform

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type OpenTelemetryConfig struct {
	ServiceName string
	Environment string
	Exporter    string
	Endpoint    string
	Insecure    bool
	Headers     map[string]string
}

type EndSpanFunc func(err error, labels Labels)

func init() {
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

func OpenTelemetryConfigFromEnv(serviceName, environment string) OpenTelemetryConfig {
	return OpenTelemetryConfig{
		ServiceName: strings.TrimSpace(firstNonEmpty(os.Getenv("OTEL_SERVICE_NAME"), serviceName)),
		Environment: strings.TrimSpace(environment),
		Exporter:    strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER"))),
		Endpoint: strings.TrimSpace(firstNonEmpty(
			os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
			os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		)),
		Insecure: boolEnvValue(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")),
		Headers:  parseOTELHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
	}
}

func InitOpenTelemetry(ctx context.Context, cfg OpenTelemetryConfig) (func(context.Context) error, error) {
	exporter := strings.ToLower(strings.TrimSpace(cfg.Exporter))
	switch exporter {
	case "", "none":
		return func(context.Context) error { return nil }, nil
	case "otlp":
	default:
		return nil, fmt.Errorf("unsupported OTEL_TRACES_EXPORTER %q", cfg.Exporter)
	}

	options := []otlptracehttp.Option{}
	if cfg.Endpoint != "" {
		options = append(options, otlptracehttp.WithEndpointURL(cfg.Endpoint))
	}
	if cfg.Insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		options = append(options, otlptracehttp.WithHeaders(cfg.Headers))
	}
	exporterInstance, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, err
	}
	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = "niceagent"
	}
	attrs := []attribute.KeyValue{
		attribute.String("service.name", serviceName),
	}
	if environment := strings.TrimSpace(cfg.Environment); environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment", environment))
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporterInstance),
		sdktrace.WithResource(resource.NewWithAttributes("", attrs...)),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func OpenTelemetryMiddleware(serviceName string, next http.Handler) http.Handler {
	tracer := otel.Tracer("niceagent/" + strings.TrimSpace(serviceName))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		spanName := strings.TrimSpace(r.Method + " " + r.URL.Path)
		if spanName == "" {
			spanName = "HTTP request"
		}
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			),
		)
		start := time.Now()
		recorder := &metricResponseWriter{ResponseWriter: w}
		defer func() {
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			span.SetAttributes(
				attribute.Int("http.response.status_code", status),
				attribute.Float64("http.server.duration_ms", float64(time.Since(start).Microseconds())/1000),
			)
			if r.Pattern != "" {
				span.SetAttributes(attribute.String("http.route", r.Pattern))
			}
			if status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			span.End()
		}()
		next.ServeHTTP(recorder, r.WithContext(ctx))
	})
}

func StartSpan(ctx context.Context, tracerName, spanName string, labels Labels) (context.Context, EndSpanFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	tracerName = strings.TrimSpace(tracerName)
	if tracerName == "" {
		tracerName = "niceagent"
	}
	spanName = strings.TrimSpace(spanName)
	if spanName == "" {
		spanName = "operation"
	}
	attrs := attributesFromLabels(labels)
	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName, trace.WithAttributes(attrs...))
	return ctx, func(err error, labels Labels) {
		if len(labels) > 0 {
			span.SetAttributes(attributesFromLabels(labels)...)
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}

func attributesFromLabels(labels Labels) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(labels))
	for key, value := range labels {
		key = sanitizeLabelName(key)
		if key == "" {
			continue
		}
		attrs = append(attrs, attribute.String(key, value))
	}
	return attrs
}

func parseOTELHeaders(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	headers := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		headers[key] = value
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func boolEnvValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
