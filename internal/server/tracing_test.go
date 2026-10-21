package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

var (
	_ http.Hijacker = (*statusRecorder)(nil)
	_ http.Flusher  = (*statusRecorder)(nil)
)

func TestNormalizeOTLPEndpoint(t *testing.T) {
	cases := map[string]string{
		"http://localhost:4318":               "http://localhost:4318/v1/traces",
		"http://localhost:4318/":              "http://localhost:4318/v1/traces",
		"https://collector.example/otlp":      "https://collector.example/otlp",
		"https://collector.example/v1/traces": "https://collector.example/v1/traces",
	}
	for in, want := range cases {
		if got := normalizeOTLPEndpoint(in); got != want {
			t.Errorf("normalizeOTLPEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTracingMiddlewareRecordsStatus(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prev)
	defer tp.Shutdown(context.Background())

	h := TracingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/stats?room=secret", nil))

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	span := spans[0]
	if span.Name() != "GET /stats" {
		t.Fatalf("span name = %q", span.Name())
	}
	if span.Status().Code != codes.Error {
		t.Fatalf("span status = %v, want Error", span.Status().Code)
	}
	var got int64 = -1
	for _, kv := range span.Attributes() {
		if kv.Key == attribute.Key("http.response.status_code") {
			got = kv.Value.AsInt64()
		}
	}
	if got != http.StatusServiceUnavailable {
		t.Fatalf("status attribute = %d", got)
	}
}
