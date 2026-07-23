package main

import (
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus instrumentation + a deliberate, config-driven failure injector.
//
// The stand mutates chart/values.yaml (env FAILURE_MODE / ERROR_RATE) to make a
// deployed revision misbehave for REAL: the pods stay up and serve traffic, but
// a controlled fraction of requests 500 or run slow. Prometheus scrapes /metrics
// and RSK0 pulls it — so the production "outcome" is genuine, not fabricated.
var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests by route and status code.",
	}, []string{"route", "code"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"route"})
)

type failConfig struct {
	mode      string  // "", "errors", "latency"  ("crash" handled in main)
	errorRate float64 // 0..1 fraction of requests to fail when mode=errors
	latency   time.Duration
}

func loadFailConfig() failConfig {
	rate, _ := strconv.ParseFloat(os.Getenv("ERROR_RATE"), 64)
	if rate <= 0 {
		rate = 0.5
	}
	ms, _ := strconv.Atoi(os.Getenv("LATENCY_MS"))
	if ms <= 0 {
		ms = 1500
	}
	return failConfig{
		mode:      os.Getenv("FAILURE_MODE"),
		errorRate: rate,
		latency:   time.Duration(ms) * time.Millisecond,
	}
}

// statusRecorder captures the status code written by the wrapped handler.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(c int) {
	r.code = c
	r.ResponseWriter.WriteHeader(c)
}

// instrument wraps a handler with metrics + the failure injector.
func instrument(route string, fc failConfig, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}

		switch fc.mode {
		case "errors":
			if rand.Float64() < fc.errorRate {
				http.Error(rec, "injected failure", http.StatusInternalServerError)
				record(route, rec.code, start)
				return
			}
		case "latency":
			time.Sleep(fc.latency)
		}

		next(rec, r)
		record(route, rec.code, start)
	}
}

func record(route string, code int, start time.Time) {
	httpRequests.WithLabelValues(route, strconv.Itoa(code)).Inc()
	httpDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
}
