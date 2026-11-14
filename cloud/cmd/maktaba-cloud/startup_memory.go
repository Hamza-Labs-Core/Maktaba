package main

import (
	"runtime"
	"runtime/debug"
)

// releaseStartupMemory frees what start-up held only for itself, once the migrations are done.
//
// goose's SQL parser keeps its 4 MB scanner buffers in a sync.Pool: the process that applied
// the migrations (the other one finds the schema at head and parses nothing) ends start-up
// holding about 16 MB of them. A pool lets go of its contents only over two collections (the
// first moves them aside, the second frees them), and an idle relay runs a collection only
// when its heap doubles or every two minutes, so for minutes its live heap
// (maktaba_go_heap_live_bytes) read 0 (no collection yet), 13.6 MB or 22 MB depending on
// where its collections fell. load-small (story 50.14) subtracts that heap before the homes
// connect and adds the idle relay's under the load, which put the same code at ≈125k, ≈134k or
// ≈150k bytes per tunnel from one run to the next.
//
// Two collections here empty the pool and leave every process's live heap at what it really
// holds; the second also returns the freed pages to the OS (start-up's peak is not kept as
// RSS). It runs once per process start, on a heap of a few MB: a few milliseconds.
func releaseStartupMemory() {
	runtime.GC()
	debug.FreeOSMemory() // a collection too: the pool's second
}
