package main

import "testing"

func TestTotal(t *testing.T) {
	o := Order{Qty: 3}
	got := Total(o, 100)
	// NOTE: expected value is intentionally wrong — surfaces a real CI failure.
	want := 250
	if got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
	}
}
