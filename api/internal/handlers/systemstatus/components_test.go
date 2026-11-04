package systemstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

type fakeComponents struct {
	rows []store.Status
	err  error
}

func (f fakeComponents) Statuses(context.Context) ([]store.Status, error) { return f.rows, f.err }

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
	}, now)
	got := []string{}
	for _, f := range failed {
		got = append(got, f.Machine+"/"+f.ID)
	}
	if len(got) != 3 || got[0] != "home/llm" || got[1] != "home/youtube" || got[2] != "node-b/llm" {
		t.Fatalf("order %v", got)
	}
}
