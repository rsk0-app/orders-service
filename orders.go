package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type Order struct {
	ID     string `json:"id"`
	Item   string `json:"item"`
	Qty    int    `json:"qty"`
	Status string `json:"status"`
}

var (
	mu     sync.Mutex
	orders = map[string]*Order{}
	nextID = 1
)

// Total returns the line total for an order given a unit price.
func Total(o Order, unitCents int) int {
	return o.Qty * unitCents
}

func registerOrderRoutes(mux *http.ServeMux, fc failConfig) {
	mux.HandleFunc("/orders", instrument("/orders", fc, downstreamGate(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Item string `json:"item"`
			Qty  int    `json:"qty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Qty <= 0 {
			http.Error(w, "invalid order", http.StatusBadRequest)
			return
		}
		// R2: with a DB, do a REAL INSERT+SELECT against the orders table; a DB
		// error surfaces as a real 500 (so a broken schema is a real failure).
		if dbEnabled() {
			o, err := dbCreateOrder(r.Context(), in.Item, in.Qty)
			if err != nil {
				http.Error(w, "order persistence failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusCreated, o)
			return
		}
		mu.Lock()
		id := fmt.Sprintf("ord_%d", nextID)
		nextID++
		o := &Order{ID: id, Item: in.Item, Qty: in.Qty, Status: "created"}
		orders[id] = o
		mu.Unlock()
		writeJSON(w, http.StatusCreated, o)
	})))

	mux.HandleFunc("/orders/", instrument("/orders/", fc, downstreamGate(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/orders/")
		if dbEnabled() {
			o, err := dbGetOrder(r.Context(), id)
			if err != nil {
				http.Error(w, "order lookup failed", http.StatusInternalServerError)
				return
			}
			if o == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, o)
			return
		}
		mu.Lock()
		o, ok := orders[id]
		mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, o)
	})))
}
