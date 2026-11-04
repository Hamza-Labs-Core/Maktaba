package systemstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

type fakeComponents struct {
	rows    []store.Status
	err     error
	desired map[string]map[string]store.Desired // by machine
	derr    error
}

func (f fakeComponents) Statuses(context.Context) ([]store.Status, error) { return f.rows, f.err }

func (f fakeComponents) Desired(_ context.Context, machine string) (map[string]store.Desired, int64, error) {
	return f.desired[machine], 1, f.derr
}

func compRow(machine, id, part, phase, reason string, since time.Time) store.Status {
	s := store.Status{Machine: machine, ID: id, Part: part, Phase: phase, Since: since}
	if reason != "" {
		s.Conditions = []store.Condition{{Type: "Ready", Status: "false", Reason: reason}}
	}
	return s
}

// Epic 51 story 51.14 T04: a failed component is attention; off, stopped, waiting and a
// fresh degraded are not.
func TestComponentsLine(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	run := func(rows []store.Status, err error) Item {
		h := &Handler{Components: fakeComponents{rows: rows, err: err}, Now: func() time.Time { return now }}
		return only(t, checkComponents(context.Background(), newEnv(h)))
	}
	calm := []store.Status{
		compRow("home", "transcription", "pipeline", "off", "", now.Add(-time.Hour)),
		compRow("home", "youtube", "pipeline", "stopped", "held", now.Add(-time.Hour)),
		compRow("home", "subtitles", "pipeline", "waiting", "dependency_off", now.Add(-time.Hour)),
		compRow("home", "classify", "pipeline", "degraded", "", now.Add(-time.Minute)),
		compRow("home", "backups", "supervisor", "running", "", now.Add(-time.Hour)),
	}
	ok := run(calm, nil)
	want(t, ok, StatusOK, "components_ok")
	if ok.Attention || ok.Reason != "All parts running." {
		t.Fatalf("calm: %+v", ok)
	}

	failed := run(append(calm, compRow("home", "llm", "supervisor", "failed", "crash_loop", now.Add(-time.Minute)),
		compRow("home", "llm", "api", "running", "", now.Add(-time.Hour))), nil)
	want(t, failed, StatusDegraded, "components_failed")
	if !failed.Attention || failed.Params["component"] != "llm" || failed.Params["why"] != "crash_loop" ||
		failed.Params["failed"] != 1 {
		t.Fatalf("failed: %+v", failed)
	}
	if failed.Reason != "1 failed: llm (crash loop)." {
		t.Fatalf("reason %q", failed.Reason)
	}
	if failed.Link != "/settings/components" {
		t.Fatalf("link %q", failed.Link)
	}

	long := run([]store.Status{compRow("node-1", "classify", "pipeline", "degraded", "", now.Add(-11*time.Minute))}, nil)
	want(t, long, StatusDegraded, "components_degraded")
	if long.Params["machine"] != "node-1" {
		t.Fatalf("degraded: %+v", long)
	}

	want(t, run(nil, errors.New("boom")), StatusDegraded, "components_unreadable")
	if items := checkComponents(context.Background(), newEnv(&Handler{})); items != nil {
		t.Fatalf("no reader, no line: %+v", items)
	}
}

func TestComponentIssuesOrder(t *testing.T) {
	now := time.Now()
	failed, _ := componentIssues([]store.Status{
		compRow("node-b", "llm", "supervisor", "failed", "crash_loop", now),
		compRow("home", "youtube", "pipeline", "failed", "", now),
		compRow("home", "llm", "supervisor", "failed", "", now),
	}, now, nil)
	got := []string{}
	for _, f := range failed {
		got = append(got, f.Machine+"/"+f.ID)
	}
	if len(got) != 3 || got[0] != "home/llm" || got[1] != "home/youtube" || got[2] != "node-b/llm" {
		t.Fatalf("order %v", got)
	}
}

// A component turned off or held never counts, whatever a part that stopped before it
// reported the change still says (smart search's index worker, failed or degraded at the
// old generation); one that is on and failed does, and so does a failed asset install.
func TestComponentsLineLeavesOutWhatIsOffOnPurpose(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	rows := []store.Status{
		compRow("home", "search.semantic", "pipeline/index", "failed", "crash_loop", now.Add(-time.Hour)),
		compRow("home", "llm", "supervisor", "degraded", "", now.Add(-time.Hour)),
		compRow("node-1", "classify", "pipeline", "failed", "", now.Add(-time.Hour)),
	}
	desired := map[string]map[string]store.Desired{
		"home": {
			"search.semantic": {ID: "search.semantic", Want: "installed", Generation: 4},
			"llm":             {ID: "llm", Want: "enabled", Hold: &store.Hold{By: "admin:test"}, Generation: 2},
		},
		"node-1": {"classify": {ID: "classify", Want: "installed", Generation: 3}},
	}
	run := func(f fakeComponents) Item {
		h := &Handler{Components: f, Now: func() time.Time { return now }}
		return only(t, checkComponents(context.Background(), newEnv(h)))
	}
	want(t, run(fakeComponents{rows: rows, desired: desired}), StatusOK, "components_ok")

	// on again: its failure is the item
	on := map[string]map[string]store.Desired{"home": desired["home"], "node-1": {"classify": {ID: "classify", Want: "enabled"}}}
	got := run(fakeComponents{rows: rows, desired: on})
	want(t, got, StatusDegraded, "components_failed")
	if got.Params["component"] != "classify" || got.Params["failed"] != 1 {
		t.Fatalf("on again: %+v", got)
	}
	// an asset's failed install counts even when its want is absent
	asset := []store.Status{compRow("home", "model.refine", "packs", "failed", "busy", now)}
	want(t, run(fakeComponents{rows: asset, desired: map[string]map[string]store.Desired{
		"home": {"model.refine": {ID: "model.refine", Want: "absent"}}}}), StatusDegraded, "components_failed")
	// desired state that cannot be read leaves nothing out
	want(t, run(fakeComponents{rows: rows, desired: desired, derr: errors.New("boom")}), StatusDegraded, "components_failed")
}
