package main

// R3 (realistic-stand): a REAL, load-driven memory footprint.
//
// Under sustained loadgen traffic the process must build a steady RSS so that a
// risky change which LOWERS resources.limits.memory BELOW that footprint gets the
// container OOMKilled by the kernel — a real, emergent failure, not an injected
// crash. The footprint MUST plateau: it is a BOUNDED ring buffer of the last N
// request payloads, so RSS climbs to ~MEM_FOOTPRINT_MB and then holds flat. An
// unbounded leak would OOM even at the baseline limit, which we never want.
//
// Sizing: each slot is a fixed 256 KiB slice; N = MEM_FOOTPRINT_MB * 4 slots
// (256 KiB * 4 = 1 MiB per MB). The retained set is ~MEM_FOOTPRINT_MB MiB once N
// requests have been served. MEM_FOOTPRINT_MB=0 disables it entirely (no
// allocation), so local/unit runs and the no-DB fallback are unaffected.
//
// The slot for a given ring position is allocated ONCE (on first use) and then
// reused IN PLACE on every later request — the payload is overwritten into the
// existing slice, never re-allocated. Allocating a fresh slice per request and
// dropping the old one churns the GC and RSS creeps up even though the live set
// is bounded; reuse-in-place keeps RSS flat at ~MEM_FOOTPRINT_MB.

import (
	"os"
	"strconv"
	"sync"
)

const footprintSlotBytes = 256 * 1024 // 256 KiB per retained payload

var (
	footprintMu   sync.Mutex
	footprintRing [][]byte
	footprintCap  int
	footprintCur  int
)

func init() {
	mb := 150
	if s := os.Getenv("MEM_FOOTPRINT_MB"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			mb = n
		}
	}
	if mb < 0 {
		mb = 0
	}
	footprintCap = mb * 4 // slots; MEM_FOOTPRINT_MB * (1 MiB / 256 KiB)
	if footprintCap > 0 {
		footprintRing = make([][]byte, footprintCap)
	}
}

// recordFootprint records one business request into the bounded ring buffer. The
// slot is allocated once (zeroed → real committed pages) and thereafter overwritten
// in place, so the retained memory climbs to ~MEM_FOOTPRINT_MB MiB and then holds
// flat — bounded, never growing unbounded. No-op when disabled.
func recordFootprint() {
	if footprintCap <= 0 {
		return
	}
	footprintMu.Lock()
	idx := footprintCur % footprintCap
	buf := footprintRing[idx]
	if buf == nil {
		buf = make([]byte, footprintSlotBytes) // first touch of this slot
		footprintRing[idx] = buf
	}
	for off := 0; off < footprintSlotBytes; off += 4096 {
		buf[off] = byte(footprintCur)
	}
	footprintCur++
	footprintMu.Unlock()
}
