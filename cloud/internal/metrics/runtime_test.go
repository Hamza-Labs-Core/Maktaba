package metrics

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var held [][]byte

// The live heap follows what the process holds: 1,000 slices of 64 KB kept across a collection raises it by
// at least that, and dropping it lowers it again. load-small (story 50.14) counts on this to
// see a per-tunnel leak: 64 KB held by each of 1,000 tunnels is this much.
func TestLiveHeapFollowsWhatIsHeld(t *testing.T) {
	runtime.GC()
	before := LiveHeapBytes()
	if before == 0 {
		t.Fatal("no live heap reading")
	}
	const kept = 1000 * 64 << 10
	for range 1000 {
		held = append(held, make([]byte, 64<<10))
	}
	runtime.GC()
	with := LiveHeapBytes()
	if with < before+kept {
		t.Errorf("holding 1,000 x 64 KB: live heap %d -> %d", before, with)
	}
	held = nil
	runtime.GC()
	if after := LiveHeapBytes(); after > with-kept*9/10 {
		t.Errorf("after letting them go: live heap %d (it was %d holding it)", after, with)
	}
}

func TestWriteGoHeap(t *testing.T) {
	var b strings.Builder
	if err := WriteGoHeap(&b); err != nil {
		t.Fatal(err)
	}
	var val string
	for line := range strings.SplitSeq(b.String(), "\n") {
		if v, ok := strings.CutPrefix(line, LiveHeapSeries+" "); ok {
			val = v
		}
	}
	if n, err := strconv.ParseUint(val, 10, 64); err != nil || n == 0 {
		t.Errorf("body:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "# TYPE "+LiveHeapSeries+" gauge\n") {
		t.Errorf("no TYPE line:\n%s", b.String())
	}
}
