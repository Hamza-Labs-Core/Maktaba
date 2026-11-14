package hostload

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestOverloaded_Rule(t *testing.T) {
	cases := []struct {
		name string
		l    Load
		want bool
	}{
		{"idle", Load{Load1: 1, Load5: 1, Cores: 28}, false},
		{"busy but within the cores", Load{Load1: 27, Load5: 26, Cores: 28}, false},
		{"both exactly twice the cores: not over", Load{Load1: 56, Load5: 56, Cores: 28}, false},
		{"a burst: the minute over, five minutes not", Load{Load1: 90, Load5: 30, Cores: 28}, false},
		{"going away: five minutes over, the minute not", Load{Load1: 20, Load5: 90, Cores: 28}, false},
		{"both over twice the cores", Load{Load1: 57, Load5: 56.5, Cores: 28}, true},
		{"load 500 on 28 cores", Load{Load1: 512, Load5: 498, Cores: 28}, true},
		{"one core", Load{Load1: 2.5, Load5: 2.1, Cores: 1}, true},
		{"cores unknown", Load{Load1: 500, Load5: 500}, false},
	}
	for _, tc := range cases {
		if got := tc.l.Overloaded(); got != tc.want {
			t.Errorf("%s: overloaded %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMonitor_NamesTheTopProgramsOnlyWhileOverloaded(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	load := Load{Load1: 3, Load5: 2, Load15: 2, Cores: 8}
	asked := 0
	m := &Monitor{
		Read: func() (Load, bool) { return load, true },
		Top: func(context.Context, int) []Proc {
			asked++
			return []Proc{{PID: 7, Name: "runaway", CPUPct: 790}}
		},
		Now: func() time.Time { return now },
	}
	st, ok := m.State(context.Background())
	if !ok || st.Overloaded || st.Cores != 8 || st.Bound != 16 || len(st.Top) != 0 || st.Top == nil || asked != 0 {
		t.Fatalf("calm: %+v ok=%v asked=%d", st, ok, asked)
	}

	// a reading is kept for cacheFor: the load changed, the answer has not yet
	load = Load{Load1: 40, Load5: 33, Load15: 9, Cores: 8}
	now = now.Add(cacheFor - time.Second)
	if st, _ = m.State(context.Background()); st.Overloaded {
		t.Fatalf("read again inside the cache time: %+v", st)
	}
	now = now.Add(2 * time.Second)
	st, ok = m.State(context.Background())
	if !ok || !st.Overloaded || st.Load1 != 40 || st.Load5 != 33 || asked != 1 ||
		len(st.Top) != 1 || st.Top[0].Name != "runaway" {
		t.Fatalf("overloaded: %+v ok=%v asked=%d", st, ok, asked)
	}
}

func TestMonitor_NoLoadAverageHere(t *testing.T) {
	m := &Monitor{Read: func() (Load, bool) { return Load{}, false }}
	if st, ok := m.State(context.Background()); ok || st.Overloaded {
		t.Fatalf("%+v ok=%v", st, ok)
	}
}

func TestMonitor_OverloadedWithNoListing(t *testing.T) {
	m := &Monitor{
		Read: func() (Load, bool) { return Load{Load1: 99, Load5: 99, Cores: 4}, true },
		Top:  func(context.Context, int) []Proc { return nil },
	}
	st, ok := m.State(context.Background())
	if !ok || !st.Overloaded || st.Top == nil || len(st.Top) != 0 {
		t.Fatalf("%+v ok=%v", st, ok)
	}
}

func TestParseTop(t *testing.T) {
	out := `    1   0.0 /sbin/launchd
  812 398.2 /Applications/Some App.app/Contents/MacOS/Some App
   77  12.5 postgres
  900 795.0 /usr/local/bin/runaway
  901   0.0 idle
 junk line
  902   3.1 /usr/bin/python3
`
	got := parseTop(out, 3)
	if len(got) != 3 || got[0].Name != "runaway" || got[0].PID != 900 || got[0].CPUPct != 795 ||
		got[1].Name != "Some App" || got[2].Name != "postgres" {
		t.Fatalf("%+v", got)
	}
	if all := parseTop(out, 10); len(all) != 4 {
		t.Fatalf("programs using no CPU are left out: %+v", all)
	}
}

// The real reading on this machine: a load average and its programs where there is one.
func TestReadLoad_OnThisMachine(t *testing.T) {
	l, ok := ReadLoad()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if ok {
			t.Fatalf("a load average on %s: %+v", runtime.GOOS, l)
		}
		return
	}
	if !ok || l.Cores < 1 || l.Load1 < 0 || l.Load5 < 0 || l.Load1 > 100000 {
		t.Fatalf("%+v ok=%v", l, ok)
	}
	if ps := TopProcs(context.Background(), 3); len(ps) > 3 {
		t.Fatalf("%+v", ps)
	}
}
