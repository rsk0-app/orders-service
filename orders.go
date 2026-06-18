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

func registerOrderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
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
		mu.Lock()
		id := fmt.Sprintf("ord_%d", nextID)
		nextID++
		o := &Order{ID: id, Item: in.Item, Qty: in.Qty, Status: "created"}
		orders[id] = o
		mu.Unlock()
		writeJSON(w, http.StatusCreated, o)
	})

	mux.HandleFunc("/orders/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/orders/")
		mu.Lock()
		o, ok := orders[id]
		mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, o)
	})
}
