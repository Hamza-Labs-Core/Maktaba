package main

import (
	"runtime"
	"sync"
	"testing"
	"weak"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
)

// pooled puts a 16 MB buffer in pool and returns a weak pointer to it.
func pooled(pool *sync.Pool) weak.Pointer[[16 << 20]byte] {
	buf := new([16 << 20]byte)
	w := weak.Make(buf)
	pool.Put(buf)
	return w
}

// A buffer start-up left in a sync.Pool (goose's parse buffers) outlives one collection, and
// releaseStartupMemory frees it: nothing of it stays in the live heap a relay reports.
func TestReleaseStartupMemoryEmptiesPools(t *testing.T) {
	var once, released sync.Pool
	kept := pooled(&once)
	runtime.GC()
	if kept.Value() == nil {
		t.Skip("this runtime frees a pool's contents in one collection: nothing to show")
	}
	gone := pooled(&released)
	releaseStartupMemory()
	if gone.Value() != nil {
		t.Fatal("the pooled buffer is still live after releaseStartupMemory")
	}
	if live := metrics.LiveHeapBytes(); live >= 16<<20 {
		t.Fatalf("live heap %d B still counts a 16 MB pooled buffer", live)
	}
	runtime.KeepAlive(&once)
	runtime.KeepAlive(&released)
}
