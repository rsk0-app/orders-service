package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestInstrumentExposesZeroBeforeRequests(t *testing.T) {
	const route = "/baseline-test"
	handler := instrument(route, failConfig{}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	read := func() map[string]float64 {
		t.Helper()
		families, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]float64{}
		for _, family := range families {
			if family.GetName() != "http_requests_total" {
				continue
			}
			for _, metric := range family.Metric {
				labels := map[string]string{}
				for _, label := range metric.Label {
					labels[label.GetName()] = label.GetValue()
				}
				if labels["route"] == route {
					values[labels["code"]] = metric.Counter.GetValue()
				}
			}
		}
		return values
	}
	before := read()
	for _, code := range []int{200, 201, 400, 404, 405, 500, 502} {
		value, ok := before[strconv.Itoa(code)]
		if !ok || value != 0 {
			t.Fatalf("missing real zero for %d: %v", code, before)
		}
	}
	if len(before) != 7 {
		t.Fatalf("unexpected series: %v", before)
	}
	handler(httptest.NewRecorder(), httptest.NewRequest("GET", route, nil))
	// Registration must never reset counters after requests have occurred.
	instrument(route, failConfig{}, func(http.ResponseWriter, *http.Request) {})
	after := read()
	if after["502"] != 1 {
		t.Fatalf("first failure lost or reset: %v", after)
	}
	for code, value := range after {
		if code != "502" && value != 0 {
			t.Fatalf("invented request: %v", after)
		}
	}
}
