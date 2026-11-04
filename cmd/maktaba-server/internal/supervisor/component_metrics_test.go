package supervisor

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

type fakeComponentRows struct {
	rows    []store.Status
	desired map[string]store.Desired
	reads   atomic.Int64
}

func (f *fakeComponentRows) StatusesOfMachine(_ context.Context, machine string) ([]store.Status, error) {
	f.reads.Add(1)
	var out []store.Status
	for _, r := range f.rows {
		if r.Machine == machine {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeComponentRows) Desired(context.Context, string) (map[string]store.Desired, int64, error) {
	return f.desired, 1, nil
}

// Story 51.14 T05: the scrape has up with a phase label for every component of this
// machine that is not absent, the restarts and the transition histogram.
func TestComponentMetrics(t *testing.T) {
	src := &fakeComponentRows{
		rows: []store.Status{
			{Machine: "home", ID: "transcription", Part: "pipeline", Phase: "running", ObservedGeneration: 2,
				Detail: map[string]any{"last_transition": map[string]any{"to": "running", "seconds": 3.2}}},
			{Machine: "home", ID: "llm", Part: "supervisor", Phase: "failed", Detail: map[string]any{"restarts": float64(3)}},
			{Machine: "home", ID: "youtube", Part: "pipeline", Phase: "off"},
			{Machine: "home", ID: "model.whisper-medium", Part: "supervisor", Phase: "not_installed"},
			{Machine: "home", ID: "someone.elses", Part: "pipeline", Phase: "running"},
			{Machine: "node-1", ID: "classify", Part: "pipeline", Phase: "running"},
		},
		desired: map[string]store.Desired{"model.whisper-medium": {ID: "model.whisper-medium", Want: "absent"}},
	}
	m := &ComponentMetrics{Source: src, Machine: "home"}
	var b strings.Builder
	if err := m.Write(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`maktaba_component_up{id="transcription",machine="home",phase="running"} 1`,
		`maktaba_component_up{id="llm",machine="home",phase="failed"} 0`,
		`maktaba_component_up{id="youtube",machine="home",phase="off"} 0`,
		`maktaba_component_restarts_total{id="llm",machine="home"} 3`,
		`maktaba_component_transition_seconds_bucket{id="transcription",to="running",le="2"} 0`,
		`maktaba_component_transition_seconds_bucket{id="transcription",to="running",le="5"} 1`,
		`maktaba_component_transition_seconds_count{id="transcription",to="running"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in\n%s", want, out)
		}
	}
	for _, never := range []string{"whisper-medium", "someone.elses", "classify", "node-1"} {
		if strings.Contains(out, never) {
			t.Errorf("%s exported:\n%s", never, out)
		}
	}
	// every line is a comment or a sample of one of the three families (the API's merge
	// parses it with expfmt: api/supervisor_metrics_test.go)
	sample := regexp.MustCompile(`^maktaba_component_(up|restarts_total|transition_seconds_(bucket|sum|count))\{[a-z]+="[^"]*"(,[a-z]+="[^"]*")*\} [0-9.e+-]+$`)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, "# ") && !sample.MatchString(line) {
			t.Errorf("not an exposition line: %q", line)
		}
	}
}

// T06: many scrapes within the TTL read the store once, and a transition is counted once.
func TestComponentMetricsCache(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	src := &fakeComponentRows{rows: []store.Status{{Machine: "home", ID: "youtube", Part: "pipeline", Phase: "running",
		ObservedGeneration: 4, Detail: map[string]any{"last_transition": map[string]any{"to": "running", "seconds": 1.0}}}}}
	m := &ComponentMetrics{Source: src, Machine: "home", Now: func() time.Time { return now }}
	for range 100 {
		var b strings.Builder
		_ = m.Write(context.Background(), &b)
		now = now.Add(10 * time.Millisecond)
	}
	if n := src.reads.Load(); n != 1 {
		t.Fatalf("%d reads in 1 s, want 1", n)
	}
	now = now.Add(ComponentMetricsTTL)
	var b strings.Builder
	_ = m.Write(context.Background(), &b)
	if src.reads.Load() != 2 {
		t.Fatal("not read again after the TTL")
	}
	if !strings.Contains(b.String(), `maktaba_component_transition_seconds_count{id="youtube",to="running"} 1`) {
		t.Fatalf("the same transition was counted again:\n%s", b.String())
	}
}

// The reconciler's rows carry the transition from the generation it acted on to the
// phase reached, and the last failure seen.
func TestReconcilerHealthDetail(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r := &Reconciler{Now: func() time.Time { return now }}
	if d := r.healthDetail("youtube", 3, "draining", ""); len(d) != 0 {
		t.Fatalf("in passing: %v", d)
	}
	now = now.Add(2500 * time.Millisecond)
	d := r.healthDetail("youtube", 3, "off", "")
	lt, _ := d["last_transition"].(map[string]any)
	if lt["to"] != "off" || lt["seconds"] != 2.5 {
		t.Fatalf("transition %v", d)
	}
	now = now.Add(time.Minute)
	if d := r.healthDetail("youtube", 3, "off", ""); d["last_transition"].(map[string]any)["seconds"] != 2.5 {
		t.Fatalf("a settled phase keeps the transition: %v", d)
	}
	d = r.healthDetail("llm", 1, "failed", "crash_loop")
	le, _ := d["last_error"].(map[string]any)
	if le["code"] != "crash_loop" {
		t.Fatalf("last error %v", d)
	}
	now = now.Add(time.Minute)
	d = r.healthDetail("llm", 1, "running", "")
	if d["last_error"].(map[string]any)["code"] != "crash_loop" {
		t.Fatalf("the last error is kept after recovering: %v", d)
	}
}
