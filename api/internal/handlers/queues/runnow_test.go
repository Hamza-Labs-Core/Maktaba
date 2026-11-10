package queues

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// Epic 59 story 59.02 T06: Run now marks a waiting job (or a library's) run_now and clears
// the quiet-hours wait only; a job that is not waiting is 404; a bad id 400.
func TestRunNow(t *testing.T) {
	f := newFixture(&queuestest.Rule{Match: "update processing_jobs", Affected: 1})
	spy := &busSpy{}
	f.h.Events = spy
	rec := f.do("POST", "/api/queues/jobs/7/run-now", "", admin())
	if rec.Code != http.StatusOK {
		t.Fatalf("job: %d %s", rec.Code, rec.Body)
	}
	var sql string
	for _, c := range f.db.Calls() {
		if c.Exec && strings.Contains(c.SQL, "update processing_jobs") {
			sql = c.SQL
		}
	}
	if !strings.Contains(sql, `"run_now": true`) || !strings.Contains(sql, "quiet_hours:%") ||
		!strings.Contains(sql, "state in ('pending', 'paused')") || !strings.Contains(sql, "and id = $1") {
		t.Errorf("run now SQL: %s", sql)
	}
	if len(spy.got) != 1 || spy.got[0].payload["by"] != "user:u1" {
		t.Errorf("events %+v", spy.got)
	}

	f = newFixture(&queuestest.Rule{Match: "update processing_jobs", Affected: 0})
	if rec := f.do("POST", "/api/queues/jobs/7/run-now", "", admin()); rec.Code != http.StatusNotFound {
		t.Errorf("not waiting: %d", rec.Code)
	}
	if rec := f.do("POST", "/api/queues/jobs/x/run-now", "", admin()); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}

	f = newFixture(&queuestest.Rule{Match: "update processing_jobs", Affected: 12})
	rec = f.do("POST", "/api/libraries/11111111-1111-4111-8111-111111111111/run-now", "", admin())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"jobs":12`) {
		t.Fatalf("library: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/api/libraries/nope/run-now", "", admin()); rec.Code != http.StatusBadRequest {
		t.Errorf("bad library: %d", rec.Code)
	}

	// a queue's: only its jobs held for the quiet hours
	f = newFixture(&queuestest.Rule{Match: "update processing_jobs", Affected: 3})
	rec = f.do("POST", "/api/queues/scan/run-now", "", admin())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"jobs":3`) {
		t.Fatalf("stage: %d %s", rec.Code, rec.Body)
	}
	sql = ""
	for _, c := range f.db.Calls() {
		if c.Exec && strings.Contains(c.SQL, "update processing_jobs") {
			sql = c.SQL
			if len(c.Args) == 0 || c.Args[0] != "scan" {
				t.Errorf("stage args %v", c.Args)
			}
		}
	}
	if !strings.Contains(sql, "and stage = $1") || !strings.Contains(sql, "paused_reason like 'quiet_hours:%'") {
		t.Errorf("stage SQL: %s", sql)
	}
}

// The waiting reason says the quiet hours, with when they open.
func TestQuietHoursWaitingReason(t *testing.T) {
	at := "01:00"
	in := stageInputs{Info: StageInfo{Stage: "scan", Enabled: true}, Served: Coverage{Workers: 1},
		Live: liveCounts{Paused: 1, Quiet: 1, QuietAt: &at}}
	s := summarizeStage(in)
	if s.WaitingReason == nil || *s.WaitingReason != WaitQuietHours || s.QuietAt == nil || *s.QuietAt != "01:00" {
		t.Fatalf("summary %+v", s)
	}
}
