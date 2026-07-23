package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	fc := loadFailConfig()

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
	registerOrderRoutes(mux, fc)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("orders-service listening on :%s (FAILURE_MODE=%q)", port, fc.mode)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
