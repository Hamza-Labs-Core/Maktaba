package queues

import (
	"database/sql/driver"
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// readOnlyRules answer the library's access as ro, the library as existing.
func readOnlyRules() []*queuestest.Rule {
	return []*queuestest.Rule{
		{Match: "select access from libraries where id = $1", Cols: []string{"access"}, Rows: [][]driver.Value{{"ro"}}},
		{Match: "select exists (select 1 from libraries where id = $1", Cols: []string{"exists"}, Rows: [][]driver.Value{{true}}},
		{Match: "l.access in ('ro', 'none')", Cols: []string{"exists"}, Rows: [][]driver.Value{{true}}},
	}
}

// T06 (story 59.03): every action that writes into a read-only library answers 409
// library_read_only, and nothing is queued (no file is touched).
func TestReadOnlyLibraryRefusesWrites(t *testing.T) {
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/libraries/" + uidA + "/convert", `{}`},
		{"POST", "/api/libraries/" + uidA + "/dedup-scan", `{}`},
		{"POST", "/api/libraries/" + uidA + "/youtube/refresh", `{}`},
		{"POST", "/api/libraries/" + uidA + "/youtube/backfill", ""},
		{"POST", "/api/libraries/" + uidA + "/youtube/retry", `{}`},
		{"POST", "/api/libraries/" + uidA + "/youtube/tartube", `{"path":"/x.json"}`},
		{"POST", "/api/libraries/" + uidA + "/youtube/sources", `{"url":"https://www.youtube.com/@x"}`},
		{"POST", "/api/queues/duplicates/" + uidA + "/apply", `{}`},
	} {
		f := newFixture(readOnlyRules()...)
		rec := f.do(c.method, c.path, c.body, admin())
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"library_read_only"`) {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
		if calls := f.db.Find("processing_jobs_enqueue"); len(calls) != 0 {
			t.Errorf("%s %s queued: %v", c.method, c.path, calls)
		}
	}
}

// "all" leaves the read-only libraries out and says how many.
func TestAllLeavesReadOnlyOut(t *testing.T) {
	f := newFixture(
		&queuestest.Rule{Match: "select id::text from libraries where deleted_at is null", Cols: []string{"id"},
			Rows: [][]driver.Value{{uidA}, {uidB}}},
		&queuestest.Rule{Match: "select access from libraries where id = $1", Cols: []string{"access"},
			Rows: [][]driver.Value{{"ro"}}, Once: true},
		&queuestest.Rule{Match: "select access from libraries where id = $1", Cols: []string{"access"},
			Rows: [][]driver.Value{{nil}}, Once: true},
		&queuestest.Rule{Match: "processing_jobs_enqueue", Cols: []string{"job_id", "outcome"},
			Rows: [][]driver.Value{{int64(7), "inserted"}}},
	)
	rec := f.do("POST", "/api/libraries/all/convert", `{"all":true}`, admin())
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out LibraryActionResponse
	decode(t, rec, &out)
	if out.SkippedReadOnly != 1 {
		t.Fatalf("response %+v", out)
	}
	var left bool
	for _, r := range out.Results {
		if r.LibraryID == uidA {
			left = r.Outcome == OutcomeLibraryReadOnly
		}
	}
	if !left {
		t.Fatalf("the read-only library has no library_read_only result: %+v", out.Results)
	}
	for _, c := range f.db.Find("processing_jobs_enqueue") {
		for _, a := range c.Args {
			if a == uidA {
				t.Fatalf("the read-only library was queued: %v", c)
			}
		}
	}
}
