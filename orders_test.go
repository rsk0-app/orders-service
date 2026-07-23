package main

import "testing"

func TestTotal(t *testing.T) {
	o := Order{Qty: 3}
	got := Total(o, 100)
	want := 300
	if got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
	}
}
