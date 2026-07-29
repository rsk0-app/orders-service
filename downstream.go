package main

// R1 (realistic-stand): real dependency call for blast-radius.
//
// On every BUSINESS request this service calls its downstream's /healthz. If the
// downstream is unhealthy (non-2xx), times out, or errors, the caller responds
// 502 — so a downstream failure cascades UP the chain instead of being masked.
//
// DOWNSTREAM_URL empty => no dependency wired; skip the call (behave as before).
// DEP_TIMEOUT_MS bounds the call so a slow/hung downstream fails fast and never
// pins a request goroutine.

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"
)

func downstreamURL() string {
	return os.Getenv("DOWNSTREAM_URL")
}

func depTimeout() time.Duration {
	ms, _ := strconv.Atoi(os.Getenv("DEP_TIMEOUT_MS"))
	if ms <= 0 {
		ms = 800
	}
	return time.Duration(ms) * time.Millisecond
}

// checkDownstream reports whether the downstream /healthz responds 2xx within
// DEP_TIMEOUT_MS. Empty DOWNSTREAM_URL => skip (always true). Never panics; every
// error (timeout, DNS, connection refused, non-2xx) collapses to false.
func checkDownstream(ctx context.Context) bool {
	base := downstreamURL()
	if base == "" {
		return true // no dependency configured — skip
	}
	c, cancel := context.WithTimeout(ctx, depTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(c, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// downstreamGate wraps a business handler: if the downstream is unavailable it
// short-circuits with 502 (cascade) before the handler runs. It stays INSIDE the
// instrument() wrapper, so the 502 is still counted in http_requests_total.
func downstreamGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkDownstream(r.Context()) {
			http.Error(w, "downstream dependency unavailable", http.StatusBadGateway)
			return
		}
		next(w, r)
	}
}
