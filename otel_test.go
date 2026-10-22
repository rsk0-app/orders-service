package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type memLogExporter struct {
	mu   sync.Mutex
	recs []sdklog.Record
}

func (m *memLogExporter) Export(_ context.Context, rs []sdklog.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rs {
		m.recs = append(m.recs, r.Clone())
	}
	return nil
}
func (m *memLogExporter) Shutdown(context.Context) error   { return nil }
func (m *memLogExporter) ForceFlush(context.Context) error { return nil }

func TestServerErrorProducesErrorSpanAndErrorLog(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prevTP)

	le := &memLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(le)))
	prevLogger := errLogger
	errLogger = slog.New(otelslog.NewHandler(serviceName, otelslog.WithLoggerProvider(lp)))
	defer func() { errLogger = prevLogger }()

	// FAILURE_MODE=errors with rate 1: every request is an injected 500.
	h := otelServer(http.HandlerFunc(instrument("/orders", failConfig{mode: "errors", errorRate: 1}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})))
	req := httptest.NewRequest("GET", "/orders/ord-12345?email=a@b.c&token=secret", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("want 500, got %d", rec.Code)
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	sp := spans[0]
	if sp.SpanKind() != oteltrace.SpanKindServer || sp.Status().Code != codes.Error {
		t.Fatalf("want SERVER span with Error status, got kind=%v status=%v", sp.SpanKind(), sp.Status().Code)
	}

	if len(le.recs) != 1 {
		t.Fatalf("want 1 log record, got %d", len(le.recs))
	}
	lr := le.recs[0]
	if lr.Severity() != otellog.SeverityError || lr.Body().AsString() != "request failed" {
		t.Fatalf("unexpected log: sev=%v body=%q", lr.Severity(), lr.Body().AsString())
	}
	if lr.TraceID() != sp.SpanContext().TraceID() {
		t.Fatalf("log not correlated to span")
	}

	// No PII: nothing from the query string or headers may appear anywhere.
	var all strings.Builder
	for _, a := range sp.Attributes() {
		all.WriteString(string(a.Key) + "=" + a.Value.Emit() + ";")
	}
	lr.WalkAttributes(func(kv otellog.KeyValue) bool {
		all.WriteString(kv.Key + "=" + kv.Value.String() + ";")
		return true
	})
	for _, bad := range []string{"secret", "a@b.c", "email", "token", "Authorization", "ord-12345"} {
		if strings.Contains(all.String(), bad) {
			t.Fatalf("attribute leaked %q: %s", bad, all.String())
		}
	}
}

func TestNoErrorLogOrErrorSpanOn2xx(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prev)
	h := otelServer(http.HandlerFunc(instrument("/orders", failConfig{}, func(w http.ResponseWriter, r *http.Request) {})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/orders", nil))
	if s := sr.Ended(); len(s) != 1 || s[0].Status().Code == codes.Error {
		t.Fatalf("2xx must not be an error span")
	}
}

func TestClientSpanOnOutgoingCall(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prev)
	// httpClient was built before the swap; rebuild its transport like prod init order does.
	old := httpClient
	httpClient = newHTTPClient()
	defer func() { httpClient = old }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	t.Setenv("DOWNSTREAM_URL", ts.URL)
	if !checkOne(context.Background(), ts.URL) {
		t.Fatal("downstream should be ready")
	}
	var client int
	for _, s := range sr.Ended() {
		if s.SpanKind() == oteltrace.SpanKindClient {
			client++
		}
	}
	if client != 1 {
		t.Fatalf("want 1 CLIENT span, got %d", client)
	}
}

// With the collector unreachable (or telemetry disabled) the service must still serve.
func TestServesWithUnreachableOrDisabledCollector(t *testing.T) {
	cases := map[string]map[string]string{
		"default endpoint (unresolvable)": {},
		"unreachable endpoint":            {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1"},
		"disabled":                        {"OTEL_SDK_DISABLED": "true"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			prevTP, prevLogger := otel.GetTracerProvider(), errLogger
			defer func() { otel.SetTracerProvider(prevTP); errLogger = prevLogger }()
			shutdown := initOTel(context.Background())
			h := otelServer(http.HandlerFunc(instrument("/orders", failConfig{mode: "errors", errorRate: 1}, func(w http.ResponseWriter, r *http.Request) {})))
			ok := otelServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
			start := time.Now()
			for i := 0; i < 20; i++ {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest("GET", "/orders", nil))
				if rec.Code != 500 {
					t.Fatalf("injected failure should still be served as 500, got %d", rec.Code)
				}
				rec = httptest.NewRecorder()
				ok.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
				if rec.Code != 200 {
					t.Fatalf("want 200, got %d", rec.Code)
				}
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("requests blocked on exporter: %v", d)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = shutdown(ctx)
		})
	}
}
