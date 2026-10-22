package main

// OpenTelemetry: traces + error logs over OTLP gRPC to the stand collector.
//
// Defaults to otel-collector.rsk0-telemetry.svc:4317 (override with
// OTEL_EXPORTER_OTLP_ENDPOINT; disable with OTEL_SDK_DISABLED=true). Exporters
// are asynchronous and batched: an unset or unreachable collector drops
// telemetry but never blocks or fails a request.
//
// No PII: span/log attributes carry only method, static route, status code.
// Bodies, query strings, headers and ids are never attached.

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const (
	serviceName     = "orders-service"
	defaultEndpoint = "otel-collector.rsk0-telemetry.svc:4317"
)

// errLogger emits error logs through the OTel log bridge. Until initOTel runs
// it uses the global (no-op) provider.
var errLogger = slog.New(otelslog.NewHandler(serviceName))

// httpClient is used for every outgoing call; it creates CLIENT spans and
// propagates trace context.
var httpClient = newHTTPClient()

func newHTTPClient() *http.Client {
	return &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
}

func otelResource() *resource.Resource {
	attrs := []attribute.KeyValue{semconv.ServiceName(serviceName)}
	if ns := os.Getenv("K8S_NAMESPACE"); ns != "" {
		attrs = append(attrs, attribute.String("k8s.namespace.name", ns))
	}
	// Merge with defaults so OTEL_RESOURCE_ATTRIBUTES still applies.
	r, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		return resource.NewSchemaless(attrs...)
	}
	return r
}

// initOTel wires the trace and log providers. It never returns an error that
// should stop startup: failures degrade to no telemetry.
func initOTel(ctx context.Context) (shutdown func(context.Context) error) {
	noop := func(context.Context) error { return nil }
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return noop
	}
	res := otelResource()

	var topts []otlptracegrpc.Option
	var lopts []otlploggrpc.Option
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		topts = append(topts, otlptracegrpc.WithEndpoint(defaultEndpoint), otlptracegrpc.WithInsecure())
		lopts = append(lopts, otlploggrpc.WithEndpoint(defaultEndpoint), otlploggrpc.WithInsecure())
	}
	topts = append(topts, otlptracegrpc.WithTimeout(3*time.Second))
	lopts = append(lopts, otlploggrpc.WithTimeout(3*time.Second))

	var fns []func(context.Context) error
	if te, err := otlptracegrpc.New(ctx, topts...); err == nil {
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(te, sdktrace.WithMaxQueueSize(2048), sdktrace.WithExportTimeout(3*time.Second)),
		)
		otel.SetTracerProvider(tp)
		fns = append(fns, tp.Shutdown)
	}
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if le, err := otlploggrpc.New(ctx, lopts...); err == nil {
		lp := sdklog.NewLoggerProvider(
			sdklog.WithResource(res),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(le, sdklog.WithExportTimeout(3*time.Second))),
		)
		otellog.SetLoggerProvider(lp)
		errLogger = slog.New(otelslog.NewHandler(serviceName, otelslog.WithLoggerProvider(lp)))
		fns = append(fns, lp.Shutdown)
	}
	return func(c context.Context) error {
		for _, f := range fns {
			_ = f(c)
		}
		return nil
	}
}

// otelServer wraps the mux with SERVER spans. Probes and metrics are skipped.
//
// otelhttp derives span attributes (path, peer address, user agent) from the
// request it is given, and paths such as /orders/<id> carry ids. So it is handed
// a sanitized copy (coarse path, no query, no peer address, no user agent) while
// the real handler still receives the original request, with the span context.
func otelServer(h http.Handler) http.Handler {
	inner := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, sr *http.Request) {
		orig, _ := sr.Context().Value(origReqKey{}).(*http.Request)
		if orig == nil {
			orig = sr
		}
		h.ServeHTTP(w, orig.WithContext(sr.Context()))
	}), serviceName,
		otelhttp.WithFilter(func(r *http.Request) bool {
			switch r.URL.Path {
			case "/healthz", "/readyz", "/metrics":
				return false
			}
			return true
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sr := r.Clone(context.WithValue(r.Context(), origReqKey{}, r))
		u := *r.URL
		u.Path, u.RawPath, u.RawQuery, u.Fragment = coarsePath(r.URL.Path), "", "", ""
		sr.URL = &u
		sr.RequestURI = u.Path
		sr.RemoteAddr = ""
		sr.Header = http.Header{}
		inner.ServeHTTP(w, sr)
	})
}

type origReqKey struct{}

// coarsePath keeps only the first path segment: /orders/ord-1 -> /orders.
func coarsePath(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return "/" + p
}

// logServerError records a 5xx as an error log (fixed message, status code and
// static route only). ctx carries the span so the log correlates to the trace.
func logServerError(ctx context.Context, route string, code int) {
	if code < 500 {
		return
	}
	errLogger.ErrorContext(ctx, "request failed",
		slog.Int("http.response.status_code", code),
		slog.String("http.route", route))
}
