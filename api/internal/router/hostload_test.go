package router

import (
	"context"
	"runtime"
	"testing"
)

// The Queues page's pulse and the System health page read one reader of this machine's
// load (HostLoad): on a system that reports its load it answers with this machine's cores
// and a bound above them, twice the same within its cache.
func TestHostLoadIsOneReaderOfThisMachine(t *testing.T) {
	if HostLoad == nil {
		t.Fatal("no reader of this machine's load")
	}
	st, ok := HostLoad.State(context.Background())
	if !ok {
		if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
			t.Fatalf("no load read on %s", runtime.GOOS)
		}
		t.Skipf("no load average on %s", runtime.GOOS)
	}
	if st.Cores < 1 || st.Bound <= float64(st.Cores) {
		t.Fatalf("cores %d, bound %v: want the bound above the cores", st.Cores, st.Bound)
	}
	again, ok := HostLoad.State(context.Background())
	if !ok || again.Load1 != st.Load1 || again.Cores != st.Cores {
		t.Fatalf("a second read within the cache differs: %+v then %+v", st, again)
	}
}
