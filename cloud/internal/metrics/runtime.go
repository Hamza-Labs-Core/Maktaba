package metrics

import (
	"fmt"
	"io"
	"runtime/metrics"
)

// liveHeap is the Go runtime's live heap: the bytes of heap objects the last garbage
// collection found reachable. It moves only when the process holds more, not with the
// garbage between collections that a container's RSS rides on, and reading it forces no
// collection, so a scrape every few seconds costs nothing.
const liveHeap = "/gc/heap/live:bytes"

// LiveHeapSeries is the series WriteGoHeap writes, read by the cloud rehearsal's load-small
// (story 50.14) for the relay's memory per tunnel.
const LiveHeapSeries = "maktaba_go_heap_live_bytes"

// LiveHeapBytes reads the process's live heap (0 if the runtime does not offer it).
func LiveHeapBytes() uint64 {
	s := []metrics.Sample{{Name: liveHeap}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

// WriteGoHeap writes the live heap as Prometheus text (a PrometheusHandler extra).
func WriteGoHeap(w io.Writer) error {
	_, err := fmt.Fprintf(w, "# HELP %s Heap bytes the last garbage collection found live.\n# TYPE %s gauge\n%s %d\n",
		LiveHeapSeries, LiveHeapSeries, LiveHeapSeries, LiveHeapBytes())
	return err
}
