package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	fc := loadFailConfig()
	shutdownOTel := initOTel(context.Background())

	// R2: connect + apply migrations BEFORE serving. A failing migration exits
	// non-zero, so a bad migration really breaks the deploy (the modeled risk).
	if err := initDB(context.Background()); err != nil {
		log.Fatalf("orders-service: db connect failed: %v", err)
	}
	if err := runMigrations(context.Background()); err != nil {
		log.Fatalf("orders-service: migrations failed: %v", err)
	}

	// "crash" mode: exit shortly after start so Kubernetes CrashLoopBackOffs the
	// pod and ArgoCD health goes Degraded — a real hard-failure outcome.
	if fc.mode == "crash" {
		after := 3
		if s := os.Getenv("CRASH_AFTER_SEC"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				after = n
			}
		}
		go func() {
			time.Sleep(time.Duration(after) * time.Second)
			log.Fatalf("FAILURE_MODE=crash: exiting after %ds", after)
		}()
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", instrument("/healthz", failConfig{}, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	// DEEP readiness probe. R2: 200 only if BOTH (a) the DB check (SELECT 1)
	// succeeds AND (b) the downstream /readyz responds 2xx within DEP_TIMEOUT_MS
	// (or no downstream configured). Either failing => 503: a broken DB or a
	// downstream outage flips this pod NotReady -> ArgoCD Degraded -> the cascade
	// propagates up the chain, WITHOUT the kubelet killing the pod (that stays on
	// shallow /healthz). Left uninstrumented (like /metrics) so frequent probes
	// don't pollute http_requests_total or trip the failure injector.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !dbHealthy(r.Context()) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable"})
			return
		}
		if checkDownstream(r.Context()) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "downstream unavailable"})
	})
	registerOrderRoutes(mux, fc)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("orders-service listening on :%s (FAILURE_MODE=%q)", port, fc.mode)
	srv := &http.Server{Addr: ":" + port, Handler: otelServer(mux)}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = shutdownOTel(ctx)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
