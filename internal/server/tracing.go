package server

import (
	"context"
	"net/http"
	"net/url"
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

	"syncwell/internal/version"
)

// TracingConfig configures the optional OTLP/HTTP trace exporter.
type TracingConfig struct {
	Endpoint    string
	Insecure    bool
	Headers     map[string]string
	SampleRate  float64
	ServiceName string
}

// InitTracing installs a global tracer provider when Endpoint is set. A no-op
// provider stays in place otherwise, so instrumentation costs almost nothing.
func InitTracing(ctx context.Context, cfg TracingConfig) (*sdktrace.TracerProvider, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil
	}
	endpoint := normalizeOTLPEndpoint(cfg.Endpoint)
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithTimeout(5 * time.Second),
	}
	if cfg.Insecure || strings.HasPrefix(endpoint, "http://") {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	rate := cfg.SampleRate
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	name := cfg.ServiceName
	if name == "" {
		name = "syncwell"
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", name),
		attribute.String("service.version", version.String()),
	))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	LogEvent("tracing_enabled", map[string]any{
		"endpoint":    endpoint,
		"sample_rate": rate,
		"service":     name,
	})
	return tp, nil
}

func normalizeOTLPEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	return u.String()
}

// TracingMiddleware starts a server span for each HTTP request, extracts W3C
// trace context from headers, and records the response status.
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer("syncwell").Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", r.Method)),
		)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))
		span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
		if rec.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
	})
}
