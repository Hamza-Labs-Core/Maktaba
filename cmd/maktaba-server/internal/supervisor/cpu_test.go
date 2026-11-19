package supervisor

import (
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 54.52: a process component's row carries its processor use (cpu_pct, the share of
// one core since the last sample, read with its memory every 5 min) and each process's
// (cpu_parts); a point or two either way rewrites nothing, so an idle machine writes no row.
func TestReconcileRowsCarryProcessorUse(t *testing.T) {
	cat := testCatalog(t, procComp("a", "fake/a*", "", ""))
	tab := &fakeTable{}
	tab.add("a", "a1", false)
	tab.add("a", "a2", false)
	tab.procs[0].PID, tab.procs[1].PID = 101, 102
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	used := map[int]time.Duration{101: time.Minute, 102: 10 * time.Second}
	reads := 0
	r.CPU = func(pids []int) map[int]time.Duration {
		reads++
		out := map[int]time.Duration{}
		for _, p := range pids {
			if d, ok := used[p]; ok {
				out[p] = d
			}
		}
		return out
	}
	d := map[string]store.Desired{"a": want("a", "enabled", 1)}
	passWith(r, d)
	// one sample: no share yet
	if row, _ := st.row("a"); row.Detail["cpu_pct"] != nil {
		t.Fatalf("a share from one sample: %v", row.Detail)
	}
	// five minutes: a1 used 150 s (half a core), a2 30 s (a tenth)
	used[101] += 150 * time.Second
	used[102] += 30 * time.Second
	now = now.Add(5 * time.Minute)
	passWith(r, d)
	row, _ := st.row("a")
	if row.Detail["cpu_pct"] != int64(60) {
		t.Fatalf("cpu_pct %v, want 60: %v", row.Detail["cpu_pct"], row.Detail)
	}
	parts, _ := row.Detail["cpu_parts"].(map[string]any)
	role := string(tab.procs[0].Role)
	if len(parts) != 2 || parts[role+"/a1"] != int64(50) || parts[role+"/a2"] != int64(10) {
		t.Fatalf("cpu_parts %v", row.Detail["cpu_parts"])
	}
	// a minute later: not read; five minutes later about the same use: no new write
	reports := st.reports
	now = now.Add(time.Minute)
	passWith(r, d)
	used[101] += 156 * time.Second // 52%
	used[102] += 30 * time.Second
	now = now.Add(4 * time.Minute)
	passWith(r, d)
	if reads != 3 || st.reports != reports {
		t.Fatalf("%d reads, %d writes after the same use, want 3 reads and none", reads, st.reports-reports)
	}
	// idle: both drop to nothing, which is written once, then never again
	now = now.Add(5 * time.Minute)
	passWith(r, d)
	if row, _ := st.row("a"); row.Detail["cpu_pct"] != int64(0) {
		t.Fatalf("idle cpu_pct %v", row.Detail["cpu_pct"])
	}
	reports = st.reports
	for range 3 {
		now = now.Add(5 * time.Minute)
		passWith(r, d)
	}
	if st.reports != reports {
		t.Fatalf("an idle machine rewrote its row %d times", st.reports-reports)
	}
	// a pid used again by a new process (its time went back): no number for it this sample
	used[101] = time.Second
	now = now.Add(5 * time.Minute)
	passWith(r, d)
	if parts, _ := mustRow(t, st, "a").Detail["cpu_parts"].(map[string]any); parts != nil {
		t.Fatalf("a reused pid has a share: %v", parts)
	}
}

func mustRow(t *testing.T, st *fakeStore, id string) store.Status {
	t.Helper()
	row, ok := st.row(id)
	if !ok {
		t.Fatalf("no row %s", id)
	}
	return row
}

// A process gone stops its component's number; a component not running has none.
func TestProcessorUseOnlyWhileRunning(t *testing.T) {
	cat := testCatalog(t, procComp("a", "fake/a*", "", ""))
	tab := &fakeTable{}
	tab.add("a", "a1", false)
	tab.procs[0].PID = 101
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	used := time.Minute
	r.CPU = func([]int) map[int]time.Duration { return map[int]time.Duration{101: used} }
	d := map[string]store.Desired{"a": want("a", "enabled", 1)}
	passWith(r, d)
	used += 3 * time.Minute
	now = now.Add(5 * time.Minute)
	passWith(r, d)
	row := mustRow(t, st, "a")
	if row.Detail["cpu_pct"] != int64(60) || row.Detail["cpu_parts"] != nil {
		t.Fatalf("one process: %v", row.Detail)
	}
	tab.procs[0].Running = false
	now = now.Add(5 * time.Minute)
	passWith(r, d)
	if v := mustRow(t, st, "a").Detail["cpu_pct"]; v != nil {
		t.Fatalf("a stopped component's cpu_pct %v", v)
	}
}

func TestCPUMoved(t *testing.T) {
	for _, c := range []struct {
		prev, now int64
		moved     bool
	}{{0, 4, false}, {0, 5, true}, {3, 0, false}, {10, 0, true}, {100, 108, false}, {100, 110, true}, {400, 430, false}} {
		if got := cpuMoved(c.prev, c.now); got != c.moved {
			t.Errorf("%d -> %d: %v", c.prev, c.now, got)
		}
	}
}
