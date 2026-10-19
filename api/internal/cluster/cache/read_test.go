package cache

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

// The readers answer the hub's history queries in the files' shapes: logs
// newest first (the hub keeps the newest page), filtered by worker, level
// (unknown levels count as info) and text; metrics oldest first, thinned
// past a limit.
func TestReaders(t *testing.T) {
	eachDB(t, func(t *testing.T, db *sql.DB, driver string) {
		clk := newClock("2026-10-19T12:00:00Z")
		c := newTestCache(db, driver, clk, 24*time.Hour)
		a := agent.Proc{WorkerID: "w-a", IP: "192.168.50.31", Host: "studio"}
		b := agent.Proc{WorkerID: "w/b", IP: "192.168.50.31", Host: "studio"}
		other := agent.Proc{WorkerID: "w-c", IP: "192.168.50.32", Host: "box"}
		c.Logs(a, []agent.LogRecord{
			{TS: "2026-10-19T08:00:01Z", Level: "debug", Event: "tick", Logger: "engine"},
			{TS: "2026-10-19T08:00:02Z", Level: "info", Event: "claimed", Logger: "engine", Fields: json.RawMessage(`{"title":"Fiqh <1>"}`)},
			{TS: "2026-10-19T08:00:03Z", Level: "warning", Event: "slow", Logger: "nas", Fields: json.RawMessage(`{"ms":900}`)},
			{TS: "2026-10-19T08:00:04Z", Level: "weird", Event: "unknown level", Logger: "x"},
		})
		c.Logs(b, []agent.LogRecord{{TS: "2026-10-19T08:00:05.123456Z", Level: "error", Event: "Boom", Logger: "engine",
			Fields: json.RawMessage(`{"cause":"nas_timeout"}`)}})
		c.Logs(other, []agent.LogRecord{{TS: "2026-10-19T08:00:06Z", Level: "error", Event: "elsewhere", Logger: "engine"}})
		c.flushLogs(bg)

		day := agent.CacheQuery{IP: "192.168.50.31", From: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC),
			To: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
		read := func(q agent.CacheQuery) []string {
			t.Helper()
			recs, err := c.ReadLogs(bg, q)
			if err != nil {
				t.Fatal(err)
			}
			out := make([]string, len(recs))
			for i, r := range recs {
				var m map[string]json.RawMessage
				must(t, json.Unmarshal(r.Line, &m))
				var ev string
				_ = json.Unmarshal(m["event"], &ev)
				out[i] = r.WorkerID + ":" + ev
			}
			return out
		}
		if got := strings.Join(read(day), ","); got != "w_b:Boom,w-a:unknown level,w-a:slow,w-a:claimed,w-a:tick" {
			t.Fatalf("all = %s", got)
		}
		q := day
		q.Limit = 2
		if got := strings.Join(read(q), ","); got != "w_b:Boom,w-a:unknown level" {
			t.Fatalf("newest 2 = %s", got)
		}
		q = day
		q.Level = "warning"
		if got := strings.Join(read(q), ","); got != "w_b:Boom,w-a:slow" {
			t.Fatalf("warning+ = %s", got)
		}
		q.Level = "info"
		if got := strings.Join(read(q), ","); got != "w_b:Boom,w-a:unknown level,w-a:slow,w-a:claimed" {
			t.Fatalf("info+ = %s", got)
		}
		q = day
		q.Q = "FIQH <1"
		if got := strings.Join(read(q), ","); got != "w-a:claimed" {
			t.Fatalf("q = %s", got)
		}
		q = day
		q.WorkerID = "w/b" // the worker id or its file name
		if got := strings.Join(read(q), ","); got != "w_b:Boom" {
			t.Fatalf("worker = %s", got)
		}
		q = day
		q.From = time.Date(2026, 10, 19, 8, 0, 3, 0, time.UTC)
		q.To = time.Date(2026, 10, 19, 8, 0, 5, 0, time.UTC)
		if got := strings.Join(read(q), ","); got != "w-a:unknown level,w-a:slow" {
			t.Fatalf("[from, to) = %s", got)
		}

		// the line is the record as the files hold it
		recs, err := c.ReadLogs(bg, agent.CacheQuery{IP: "192.168.50.31", WorkerID: "w_b"})
		if err != nil || len(recs) != 1 {
			t.Fatalf("%v %v", recs, err)
		}
		if got := string(recs[0].Line); got != `{"ts":"2026-10-19T08:00:05.123456Z","level":"error","event":"Boom","logger":"engine","fields":{"cause":"nas_timeout"}}` {
			t.Fatalf("line = %s", got)
		}
		if !recs[0].TS.Equal(time.Date(2026, 10, 19, 8, 0, 5, 123456000, time.UTC)) {
			t.Fatalf("ts = %s", recs[0].TS)
		}

		// metrics: oldest first, one per process per slot, thinned past the limit
		for s := 0; s < 120; s += 2 {
			at := time.Date(2026, 10, 19, 9, 0, s, 0, time.UTC)
			clk.Set(at)
			c.Metrics(a, frame(at, float64(s)), nil, false)
			c.Metrics(b, frame(at, float64(s)), nil, false)
		}
		c.flushMetrics(bg)
		mq := agent.CacheQuery{IP: "192.168.50.31", From: day.From, To: day.To}
		all, err := c.ReadMetrics(bg, mq)
		if err != nil || len(all) != 24 {
			t.Fatalf("metrics = %d %v", len(all), err)
		}
		for i := 1; i < len(all); i++ {
			if all[i].TS.Before(all[i-1].TS) {
				t.Fatal("metrics not oldest first")
			}
		}
		var f map[string]any
		must(t, json.Unmarshal(all[0].Line, &f))
		if f["ts"] != "2026-10-19T09:00:00Z" || f["t"] != nil || all[0].WorkerID != "w-a" {
			t.Fatalf("first frame = %s (%s)", all[0].Line, all[0].WorkerID)
		}
		mq.Limit = 10
		thin, err := c.ReadMetrics(bg, mq)
		if err != nil || len(thin) != 8 || !thin[0].TS.Equal(all[0].TS) {
			t.Fatalf("thinned = %d %v", len(thin), err)
		}
		mq.Limit, mq.WorkerID = 0, "w-a"
		if one, _ := c.ReadMetrics(bg, mq); len(one) != 12 {
			t.Fatalf("one worker = %d", len(one))
		}

		// a window of 0 reads nothing
		off := newTestCache(db, driver, clk, 0)
		if r, err := off.ReadLogs(bg, day); err != nil || len(r) != 0 {
			t.Fatal("window 0 read logs")
		}
		if r, err := off.ReadMetrics(bg, mq); err != nil || len(r) != 0 {
			t.Fatal("window 0 read metrics")
		}
	})
}

// Covers never claims a range the cache does not hold.
func TestCovers(t *testing.T) {
	db := sqliteDB(t)
	clk := newClock("2026-10-19T12:00:00Z")
	c := newTestCache(db, "sqlite", clk, 24*time.Hour)
	now := clk.Now()
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if c.Covers(KindLogs, day, day.Add(24*time.Hour)) {
		t.Fatal("no pass yet")
	}
	c.passAt[KindLogs] = now
	for _, tc := range []struct {
		kind     string
		from, to time.Time
		want     bool
	}{
		{KindLogs, day, day.Add(24 * time.Hour), true},                       // today, to the end of the day
		{KindLogs, now.Add(-24 * time.Hour), now, true},                      // the whole window
		{KindLogs, now.Add(-24*time.Hour - time.Second), now, false},         // before the window
		{KindLogs, day.Add(-24 * time.Hour), day, false},                     // yesterday
		{KindMetrics, day, day.Add(24 * time.Hour), false},                   // no pass for metrics
		{"jobs", day, day.Add(24 * time.Hour), false},                        // never cached
		{KindLogs, now.Add(-48 * time.Hour), now.Add(-48 * time.Hour), true}, // an empty range
	} {
		if got := c.Covers(tc.kind, tc.from, tc.to); got != tc.want {
			t.Errorf("Covers(%s, %s, %s) = %v", tc.kind, tc.from, tc.to, got)
		}
	}
	clk.Add(3*time.Minute + time.Second)
	if c.Covers(KindLogs, day, day.Add(24*time.Hour)) {
		t.Fatal("the last pass is stale")
	}
	if st := c.Snapshot(); st.Logs.WarmFrom != nil || st.Logs.PassAt == nil {
		t.Fatalf("stats = %+v", st.Logs)
	}
}
