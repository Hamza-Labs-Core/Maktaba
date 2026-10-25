package updates

import (
	"context"
	"testing"
	"time"
)

// TC3: the home writes update.state and update.machines_behind into health_samples every
// minute: 0 up to date, 1 a release waits, 2 a run is under way, 3 the last run ended
// badly (a day at most).
func TestHealthSamplesUpdateState(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	if _, err := r.f.db.Exec(`CREATE TABLE IF NOT EXISTS health_samples (at TEXT NOT NULL, host TEXT NOT NULL,
		metric TEXT NOT NULL, value REAL NOT NULL, PRIMARY KEY (metric, host, at))`); err != nil {
		t.Fatal(err)
	}
	r.node(t, "n9", "zulu", "linux-amd64", "1.3.0")
	if err := r.f.store.RaiseWatermark(ctx, "1.4.0"); err != nil {
		t.Fatal(err)
	}
	behind := false
	h := &HealthSampler{Metrics: NewMetrics(r.d, func() string { return "1.4.0" }), Host: "home-box",
		Behind: func(context.Context) bool { return behind }}
	sample := func() (state, machines float64) {
		t.Helper()
		if err := h.Sample(ctx); err != nil {
			t.Fatal(err)
		}
		row := func(metric string) float64 {
			var v float64
			if err := r.f.db.QueryRow(`SELECT value FROM health_samples WHERE metric = $1 AND host = 'home-box'
				ORDER BY at DESC LIMIT 1`, metric).Scan(&v); err != nil {
				t.Fatalf("%s: %v", metric, err)
			}
			return v
		}
		r.f.now = r.f.now.Add(time.Minute)
		return row(SampleUpdateState), row(SampleMachinesBehind)
	}
	if s, m := sample(); s != UpdateStateUpToDate || m != 1 {
		t.Fatalf("up to date: state %v, behind %v", s, m)
	}
	behind = true
	if s, _ := sample(); s != UpdateStateBehind {
		t.Fatalf("a release waits: state %v", s)
	}
	run, err := r.f.store.InsertRun(ctx, "1.5.0", "1.4.0", TriggerAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := sample(); s != UpdateStateUpdating {
		t.Fatalf("a run: state %v", s)
	}
	if err := r.f.store.FinishRun(ctx, run.ID, RunRolledBack, map[string]any{"code": "health"}); err != nil {
		t.Fatal(err)
	}
	if s, _ := sample(); s != UpdateStateTrouble {
		t.Fatalf("a rolled back run: state %v", s)
	}
	r.f.now = r.f.now.Add(troubleFor)
	if s, _ := sample(); s != UpdateStateBehind {
		t.Fatalf("a day after the rollback: state %v", s)
	}
}
