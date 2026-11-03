package queues

import (
	"database/sql/driver"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

func TestStageHealth(t *testing.T) {
	cfg := defaultQueueHealthRules()
	sig := func(s, cls string, n int) windowFailure {
		return windowFailure{Signature: s, Class: cls, Count: n, Example: "ex " + s}
	}
	tests := []struct {
		name     string
		in       healthInputs
		state    string
		needsYou int
		failing  string
	}{
		{"a small share stays neutral", healthInputs{
			Failures: []windowFailure{sig("boom:x", "unknown", 15)}, DoneWindow: 5000,
			FailedOpen: map[string]int{"file_missing": 853}}, HealthOK, 853, ""},
		{"a dominant new signature is red", healthInputs{
			Failures: []windowFailure{sig("boom:x", "unknown", 40), sig("nas_timeout", "transient", 3)}, DoneWindow: 100,
			FailedOpen: map[string]int{"boom:x": 40, "ffmpeg_decode": 2}}, HealthFailing, 2, "boom:x"},
		{"too few of one signature is not red", healthInputs{
			Failures: []windowFailure{sig("a", "unknown", 5), sig("b", "unknown", 5)}, DoneWindow: 10}, HealthOK, 0, ""},
		{"explained classes never turn it red", healthInputs{
			Failures:   []windowFailure{sig("ffmpeg_decode", "item_permanent", 50), sig("model_unavailable", "dependency:model:tagcheck", 500)},
			DoneWindow: 1}, HealthOK, 0, ""},
		{"parked jobs make it wait", healthInputs{
			Waiting: []WaitingOn{{On: "dependency:model:tagcheck", Count: 6577}}}, HealthWaiting, 0, ""},
		{"red wins over waiting", healthInputs{
			Failures: []windowFailure{sig("boom", "", 20)}, Waiting: []WaitingOn{{On: "resource:shm", Count: 2}}}, HealthFailing, 0, "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stageHealth(tc.in, cfg)
			if got.State != tc.state || got.NeedsYou != tc.needsYou {
				t.Fatalf("state %q needs_you %d, want %q %d (%+v)", got.State, got.NeedsYou, tc.state, tc.needsYou, got)
			}
			if tc.failing != "" && (got.Failing == nil || got.Failing.Signature != tc.failing || got.Failing.Example == "") {
				t.Fatalf("failing = %+v", got.Failing)
			}
			if tc.failing == "" && got.Failing != nil {
				t.Fatalf("not failing, got %+v", got.Failing)
			}
			if got.Waiting == nil {
				t.Fatal("waiting must be a list")
			}
		})
	}
}

// The API's defaults are the pipeline's (schema.json is generated from it).
func TestQueueHealthDefaultsMatchTheSchema(t *testing.T) {
	b, err := os.ReadFile("../rules/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sections []struct {
			Key    string `json:"key"`
			Fields []struct {
				Name    string          `json:"name"`
				Default json.RawMessage `json:"default"`
			} `json:"fields"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	want := defaultQueueHealthRules()
	found := 0
	for _, s := range doc.Sections {
		if s.Key != "queue_health" {
			continue
		}
		for _, f := range s.Fields {
			var v float64
			_ = json.Unmarshal(f.Default, &v)
			switch f.Name {
			case "window_hours":
				found++
				if v != want.WindowHours {
					t.Errorf("window_hours %v != %v", v, want.WindowHours)
				}
			case "ok_failure_share":
				found++
				if v != want.OKFailureShare {
					t.Errorf("ok_failure_share %v != %v", v, want.OKFailureShare)
				}
			case "failing_min_jobs":
				found++
				if int(v) != want.FailingMinJobs {
					t.Errorf("failing_min_jobs %v != %v", v, want.FailingMinJobs)
				}
			}
		}
	}
	if found != 3 {
		t.Fatalf("schema.json has %d of the 3 queue_health rules the API reads", found)
	}
}

func TestQueueHealthRulesFromSettings(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "rules.queue_health", Cols: []string{"value"},
		Rows: [][]driver.Value{{`{"window_hours": 2, "failing_min_jobs": 3, "ok_failure_share": 7}`}}})
	got := f.h.loadQueueHealthRules(t.Context())
	if got.WindowHours != 2 || got.FailingMinJobs != 3 || got.OKFailureShare != 0.02 {
		t.Fatalf("rules = %+v (a share above 1 keeps the default)", got)
	}
}

func TestSnapshotCarriesTheTileHealth(t *testing.T) {
	rules := append(snapshotRules(),
		&queuestest.Rule{Match: "from job_events wf", Cols: []string{"stage", "cause", "class", "n", "msg", "subj"},
			Rows: [][]driver.Value{{"probe", "boom:x", "unknown", int64(30), "boom: x", "video:" + uidA}}},
		&queuestest.Rule{Match: "from processing_jobs d", Cols: []string{"stage", "n"},
			Rows: [][]driver.Value{{"probe", int64(10)}}},
		&queuestest.Rule{Match: "left join dependency_health dh", Cols: []string{"stage", "on", "n", "healthy", "msg", "since"},
			Rows: [][]driver.Value{{"transcribe", "dependency:model:tagcheck", int64(6577), false, "not answering", t0}}},
	)
	f := newFixture(rules...)
	rec := f.do("GET", "/api/queues", "", admin())
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	decode(t, rec, &snap)
	by := map[string]*StageHealth{}
	for _, q := range snap.Queues {
		by[q.Stage] = q.Health
	}
	if p := by["probe"]; p == nil || p.State != HealthFailing || p.Failing.Signature != "boom:x" {
		t.Fatalf("probe health = %+v", p)
	}
	// nas_timeout (4 open, from the shared rules) is not the red signature: it needs you
	if by["probe"].NeedsYou != 4 {
		t.Errorf("probe needs_you = %d", by["probe"].NeedsYou)
	}
	if tr := by["transcribe"]; tr == nil || tr.State != HealthWaiting || tr.Waiting[0].Count != 6577 {
		t.Fatalf("transcribe health = %+v", tr)
	}
	if !strings.Contains(f.db.Find("from failure_ignores fi")[0].SQL, "not exists") {
		t.Error("open failures do not leave the ignored items out")
	}
}

// Epic 51 story 51.14 T01: a stage whose only parked jobs wait on a component, or whose
// component an admin turned off, is off (grey), never waiting.
func TestStageHealth_ComponentOff(t *testing.T) {
	cfg := defaultQueueHealthRules()
	parked := stageHealth(healthInputs{Waiting: []WaitingOn{{On: "component:transcription", Count: 1840}}}, cfg)
	if parked.State != HealthOff {
		t.Fatalf("parked on a component: state %q, want off", parked.State)
	}
	if w := parked.Waiting[0]; w.Component != "transcription" || w.Phase != "off" {
		t.Fatalf("waiting = %+v", w)
	}
	held := stageHealth(healthInputs{Component: &componentHold{ID: "transcription", Phase: "stopped", Queued: 1840}}, cfg)
	if held.State != HealthOff || len(held.Waiting) != 1 {
		t.Fatalf("held: %+v", held)
	}
	if w := held.Waiting[0]; w.On != "component:transcription" || w.Count != 1840 || w.Phase != "stopped" {
		t.Fatalf("held waiting = %+v", w)
	}
	// a share parked beside the component still makes it waiting on that share
	mixed := stageHealth(healthInputs{Waiting: []WaitingOn{{On: "component:transcription", Count: 3},
		{On: "resource:disk", Count: 2}}}, cfg)
	if mixed.State != HealthWaiting {
		t.Fatalf("mixed: state %q, want waiting", mixed.State)
	}
}

// T02: a stage can fail and be off; failing wins.
func TestStageHealth_FailingWinsOverOff(t *testing.T) {
	got := stageHealth(healthInputs{
		Failures:  []windowFailure{{Signature: "boom", Class: "unknown", Count: 40}},
		Component: &componentHold{ID: "transcription", Phase: "off", Queued: 5},
	}, defaultQueueHealthRules())
	if got.State != HealthFailing || got.Failing == nil {
		t.Fatalf("state %q, want failing", got.State)
	}
}

// componentHoldOf reads only the component's reasons; an operator's switch is not one.
func TestComponentHoldOf(t *testing.T) {
	r := func(s string) *string { return &s }
	if h := componentHoldOf(StageInfo{Stage: "transcribe", DisabledReason: r("component_off")}, 7); h == nil ||
		h.ID != "transcription" || h.Phase != "off" || h.Queued != 7 {
		t.Fatalf("off: %+v", h)
	}
	if h := componentHoldOf(StageInfo{Stage: "transcribe", DisabledReason: r("component_held")}, 0); h == nil || h.Phase != "stopped" {
		t.Fatalf("held: %+v", h)
	}
	if h := componentHoldOf(StageInfo{Stage: "transcribe", DisabledReason: r("operator")}, 0); h != nil {
		t.Fatalf("operator: %+v", h)
	}
	if h := componentHoldOf(StageInfo{Stage: "transcribe", Enabled: true, DisabledReason: r("component_off")}, 0); h != nil {
		t.Fatalf("enabled: %+v", h)
	}
}

// T03: the tile of a stage its component holds back is off with the queued count, and
// the overview lists it in Off, not in Waiting.
func TestSnapshotAndRollup_ComponentOffStage(t *testing.T) {
	rules := snapshotRules()
	rules[0] = stagesRule(
		stageRow("probe", "episodes", 1, "nas_read", withEdges(`[{"to":"detect","map":"library_of"}]`)),
		stageRow("detect", "seasons", 1, "cpu", withKinds("{library}")),
		stageRow("transcribe", "media", 2, "gpu", withDisabled("component_off")),
	)
	rules = append(rules, &queuestest.Rule{Match: "left join dependency_health dh",
		Cols: []string{"stage", "on", "n", "healthy", "msg", "since"},
		Rows: [][]driver.Value{{"detect", "resource:disk", int64(4), nil, nil, nil}}})
	f := newFixture(rules...)
	rec := f.do("GET", "/api/queues", "", admin())
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	decode(t, rec, &snap)
	for _, q := range snap.Queues {
		if q.Stage != "transcribe" {
			continue
		}
		if q.Health == nil || q.Health.State != HealthOff {
			t.Fatalf("transcribe health = %+v", q.Health)
		}
		if w := q.Health.Waiting[0]; w.Component != "transcription" || w.Count != 3 || w.Phase != "off" {
			t.Fatalf("transcribe waiting = %+v", w)
		}
	}
	r, err := f.h.HealthRollup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Off) != 1 || r.Off[0] != "transcribe" {
		t.Fatalf("off = %v", r.Off)
	}
	if len(r.Waiting) != 1 || r.Waiting[0] != "detect" {
		t.Fatalf("waiting = %v (an off stage is not waiting)", r.Waiting)
	}
}
