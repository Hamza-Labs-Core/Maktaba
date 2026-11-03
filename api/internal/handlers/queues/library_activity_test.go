package queues

import (
	"database/sql/driver"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

func activityRule(rows ...[]driver.Value) *queuestest.Rule {
	return &queuestest.Rule{Match: "(finished_at at time zone 'utc')::date",
		Cols: []string{"lib", "day", "done", "failed"}, Rows: rows}
}

// TestLibraryActivity: a library's finished jobs per UTC day, oldest first,
// aligned with days; days outside the window are dropped; cached for a minute.
func TestLibraryActivity(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	f := newFixture(activityRule(
		[]driver.Value{uidA, day(15), int64(40), int64(2)},
		[]driver.Value{uidA, day(13), int64(7), int64(0)},
		[]driver.Value{uidB, day(14), int64(1), int64(1)},
		[]driver.Value{uidB, day(1), int64(99), int64(0)}, // before the window
	))
	rec := f.do("GET", "/api/queues/libraries/activity?days=3", "", admin())
	var out LibraryActivityResponse
	decode(t, rec, &out)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(out.Days) != 3 || out.Days[0] != "2026-10-13" || out.Days[2] != "2026-10-15" {
		t.Fatalf("days = %v", out.Days)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %+v", out.Items)
	}
	a, b := out.Items[0], out.Items[1]
	if a.LibraryID != uidA || a.Done[0] != 7 || a.Done[2] != 40 || a.Failed[2] != 2 || a.Done[1] != 0 {
		t.Errorf("a = %+v", a)
	}
	if b.LibraryID != uidB || b.Done[1] != 1 || b.Failed[1] != 1 || b.Done[0] != 0 {
		t.Errorf("b = %+v", b)
	}
	calls := f.db.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if got := calls[0].Args[0]; got != day(13) {
		t.Errorf("window starts %v", got)
	}

	// a minute's cache per window
	f.do("GET", "/api/queues/libraries/activity?days=3", "", admin())
	if len(f.db.Calls()) != 1 {
		t.Errorf("second call within a minute must come from the cache")
	}
	f.h.NowFunc = func() time.Time { return t0.Add(2 * time.Minute) }
	f.do("GET", "/api/queues/libraries/activity?days=3", "", admin())
	if len(f.db.Calls()) != 2 {
		t.Errorf("an expired cache must read again")
	}
}

func TestLibraryActivity_DefaultAndBadDays(t *testing.T) {
	f := newFixture(activityRule())
	rec := f.do("GET", "/api/queues/libraries/activity", "", admin())
	var out LibraryActivityResponse
	decode(t, rec, &out)
	if rec.Code != http.StatusOK || len(out.Days) != 14 || out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("%d %+v", rec.Code, out)
	}
	for _, q := range []string{"0", "61", "x"} {
		rec := f.do("GET", "/api/queues/libraries/activity?days="+q, "", admin())
		if rec.Code != http.StatusBadRequest {
			t.Errorf("days=%s: %d", q, rec.Code)
		}
	}
}
