package main

// R1 (realistic-stand): real dependency calls for blast-radius.
//
// R5 FAN-OUT: orders-service now depends on TWO downstreams — payments-api
// (DOWNSTREAM_URL) AND inventory-service (INVENTORY_URL). On every BUSINESS
// request it calls both; if EITHER is unhealthy (non-2xx), times out, or errors,
// the caller responds 502 — so a failure in EITHER downstream cascades UP the
// chain (deeper blast-radius) instead of being masked.
//
// DOWNSTREAM_URL / INVENTORY_URL empty => that dependency is not wired; skip it.
// DEP_TIMEOUT_MS bounds each call so a slow/hung downstream fails fast and never
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

func inventoryURL() string {
	return os.Getenv("INVENTORY_URL")
}

func depTimeout() time.Duration {
	ms, _ := strconv.Atoi(os.Getenv("DEP_TIMEOUT_MS"))
	if ms <= 0 {
		ms = 800
	}
	return time.Duration(ms) * time.Millisecond
}

// checkOne reports whether base's DEEP /readyz responds 2xx within DEP_TIMEOUT_MS.
// Empty base => skip (always true). Never panics; every error (timeout, DNS,
// connection refused, non-2xx) collapses to false.
//
// We probe each downstream's DEEP /readyz (not shallow /healthz) so readiness
// CHAINS: if a downstream is down, orders /readyz goes 503, which makes checkout
// /readyz go 503 too — the cascade propagates ALL the way up the chain. The leaf
// (payments / inventory) serves /readyz as its own deep check (DB, etc.).
func checkOne(ctx context.Context, base string) bool {
	if base == "" {
		return true // no dependency configured — skip
	}
	c, cancel := context.WithTimeout(ctx, depTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(c, http.MethodGet, base+"/readyz", nil)
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

// checkDownstream reports whether ALL wired downstreams (payments AND inventory)
// are ready. The two are probed in PARALLEL so their timeouts overlap rather than
// stack. ANY failing => false => orders /readyz 503 (and, on the business path, a
// 502). This is the R5 fan-out: orders is NotReady if EITHER payments OR inventory
// is down.
func checkDownstream(ctx context.Context) bool {
	targets := []string{downstreamURL(), inventoryURL()}
	results := make(chan bool, len(targets))
	for _, base := range targets {
		go func(b string) { results <- checkOne(ctx, b) }(base)
	}
	ok := true
	for range targets {
		if !<-results {
			ok = false
		}
	}
	return ok
}

// downstreamGate wraps a business handler: if any downstream is unavailable it
// short-circuits with 502 (cascade) before the handler runs. It stays INSIDE the
// instrument() wrapper, so the 502 is still counted in http_requests_total.
func downstreamGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// R3: accumulate the real, bounded memory footprint on the business path
		// only (downstreamGate wraps just the /orders* business handlers, never
		// /healthz, /readyz or /metrics). Under load RSS plateaus at ~MEM_FOOTPRINT_MB.
		recordFootprint()
		if !checkDownstream(r.Context()) {
			http.Error(w, "downstream dependency unavailable", http.StatusBadGateway)
			return
		}
		next(w, r)
	}
}
