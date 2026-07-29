package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// R5 fan-out: checkDownstream must be true ONLY when BOTH payments AND inventory
// /readyz are 2xx; either one failing => false (=> orders /readyz 503, business 502).
func TestCheckDownstreamFanOut(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ready.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()

	cases := []struct {
		name          string
		payments, inv string
		want          bool
	}{
		{"both ready", ready.URL, ready.URL, true},
		{"payments down", down.URL, ready.URL, false},
		{"inventory down", ready.URL, down.URL, false},
		{"both down", down.URL, down.URL, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DOWNSTREAM_URL", c.payments)
			t.Setenv("INVENTORY_URL", c.inv)
			if got := checkDownstream(context.Background()); got != c.want {
				t.Fatalf("checkDownstream() = %v, want %v", got, c.want)
			}
		})
	}
}
