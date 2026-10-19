package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

type hsRow struct {
	at           time.Time
	host, metric string
	value        float64
}

func healthRows(t *testing.T, db *sql.DB, where string, args ...any) []hsRow {
	t.Helper()
	q := `SELECT at, host, metric, value FROM health_samples`
	if where != "" {
		q += ` WHERE ` + where
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []hsRow
	for rows.Next() {
		var r hsRow
		var at dbTime
		if err := rows.Scan(&at, &r.host, &r.metric, &r.value); err != nil {
			t.Fatal(err)
		}
		r.at = at.T
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		if a.host != b.host {
			return a.host < b.host
		}
		return a.metric < b.metric
	})
	return out
}

func (r hsRow) String() string {
	return fmt.Sprintf("%s %s %s=%g", r.at.Format("15:04:05.000"), r.host, r.metric, r.value)
}

func rowsString(rs []hsRow) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = r.String()
	}
	return strings.Join(s, "\n")
}

func frame(ts time.Time, cpu float64) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"ts": ts.Format(time.RFC3339Nano), "system": map[string]any{"cpu_pct": cpu}})
	return b
}

// Each process's health numbers reach health_samples at most once per 10 s
// flush (the latest map wins), at the hub's clock, under the process's
// host and today's metric names; two processes on one host keep apart
// rows; nas.reads_busy is counted once per flush for every host; a final
// map is written at once.
func TestHealthCadenceAndShape(t *testing.T) {
	eachDB(t, func(t *testing.T, db *sql.DB, driver string) {
		clk := newClock("2026-10-19T08:00:00Z")
		c := newTestCache(db, driver, clk, 24*time.Hour)
		wantBusy := float64(0)
		if driver == "sqlite" {
			for _, s := range []string{
				`INSERT INTO job_stages (stage, pool) VALUES ('probe', 'nas_read'), ('extract', 'nas_read'), ('tag', 'cpu')`,
				`INSERT INTO processing_jobs (stage, state) VALUES ('probe','running'), ('extract','claimed'),
				   ('probe','pending'), ('tag','running'), ('extract','resuming')`,
			} {
				if _, err := db.Exec(s); err != nil {
					t.Fatal(err)
				}
			}
			wantBusy = 3
		} else if err := db.QueryRow(readsBusySQL).Scan(&wantBusy); err != nil {
			t.Fatal(err)
		}
		a, b, cc := proc("w-a", "h1", "10.0.0.1"), proc("w-b", "h1", "10.0.0.1"), proc("w-c", "h2", "10.0.0.2")
		start := clk.Now()
		for s := 0; s <= 10; s += 2 {
			clk.Set(start.Add(time.Duration(s) * time.Second))
			for i, p := range []agent.Proc{a, b, cc} {
				c.Metrics(p, nil, map[string]float64{"cpu.pct": float64(s*10 + i), "nas.timeouts": 1,
					"stt.audio_sec:" + p.WorkerID: float64(s), "bad": math.NaN()}, false)
			}
		}
		c.flushMetrics(bg) // t = 10 s
		got := healthRows(t, db, "")
		t10 := start.Add(10 * time.Second)
		want := []hsRow{
			{t10, "h1", "cpu.pct", 100}, {t10, "h1", "nas.reads_busy", wantBusy}, {t10, "h1", "nas.timeouts", 1},
			{t10, "h1", "stt.audio_sec:w-a", 10},
			{t10, "h2", "cpu.pct", 102}, {t10, "h2", "nas.reads_busy", wantBusy}, {t10, "h2", "nas.timeouts", 1},
			{t10, "h2", "stt.audio_sec:w-c", 10},
			// w-b got its map in the same millisecond on the same host: a millisecond on
			{t10.Add(time.Millisecond), "h1", "cpu.pct", 101}, {t10.Add(time.Millisecond), "h1", "nas.timeouts", 1},
			{t10.Add(time.Millisecond), "h1", "stt.audio_sec:w-b", 10},
		}
		if rowsString(got) != rowsString(want) {
			t.Fatalf("after the first flush:\n%s\nwant\n%s", rowsString(got), rowsString(want))
		}

		// a final map is written at once, alone
		clk.Set(start.Add(12 * time.Second))
		c.Metrics(b, nil, map[string]float64{"cpu.pct": 7}, false)
		clk.Set(start.Add(13 * time.Second))
		c.Metrics(a, nil, map[string]float64{"cpu.pct": 99, "stt.audio_sec:w-a": 13}, true)
		select {
		case <-c.wake:
		default:
			t.Fatal("a final map must wake the writer")
		}
		c.flushFinal(bg)
		t13 := start.Add(13 * time.Second)
		if got := healthRows(t, db, "at > $1", c.healthTS(t10.Add(time.Millisecond))); rowsString(got) != rowsString([]hsRow{
			{t13, "h1", "cpu.pct", 99}, {t13, "h1", "nas.reads_busy", wantBusy}, {t13, "h1", "stt.audio_sec:w-a", 13},
		}) {
			t.Fatalf("final:\n%s", rowsString(got))
		}
		// w-b's waiting map goes with the next 10 s flush
		clk.Set(start.Add(20 * time.Second))
		c.flushMetrics(bg)
		if got := healthRows(t, db, "at > $1 AND metric = 'cpu.pct'", c.healthTS(t10.Add(time.Millisecond))); len(got) != 2 ||
			got[0].value != 7 || !got[0].at.Equal(start.Add(12*time.Second)) || got[1].value != 99 {
			t.Fatalf("w-b's map:\n%s", rowsString(got))
		}
		// nothing waits: a flush writes nothing more
		n := count(t, db, `SELECT count(*) FROM health_samples`)
		clk.Set(start.Add(30 * time.Second))
		c.flushMetrics(bg)
		if count(t, db, `SELECT count(*) FROM health_samples`) != n {
			t.Fatal("an empty flush wrote rows")
		}
		st := c.Snapshot()
		if st.Health.Written != int64(n) || st.Health.Dropped != 0 || st.Health.Queued != 0 {
			t.Fatalf("health stats = %+v (rows %d)", st.Health, n)
		}
	})
}

// A process's metrics frames are cached one per 10 s slot (the first of
// each); its log records all; Window 0 caches neither, and still writes
// health_samples.
func TestChannelToTables(t *testing.T) {
	eachDB(t, func(t *testing.T, db *sql.DB, driver string) {
		clk := newClock("2026-10-19T08:00:00Z")
		c := newTestCache(db, driver, clk, 24*time.Hour)
		p := agent.Proc{SessionID: "s1", WorkerID: "studio/4242/ab12cd34", NodeID: "8f6d0a52-0000-4000-8000-000000000001",
			Host: "studio", IP: "fe80::1"}
		start := clk.Now()
		for s := 0; s < 30; s += 2 {
			at := start.Add(time.Duration(s) * time.Second)
			clk.Set(at)
			c.Metrics(p, frame(at, float64(s)), nil, false)
		}
		c.Logs(p, []agent.LogRecord{
			{TS: "2026-10-19T08:00:01.250Z", Level: "info", Event: "claimed", Logger: "engine", Fields: json.RawMessage(`{"job":1}`)},
			{TS: "2026-10-19T08:00:02Z", Level: "WARNING", Event: "slow <nas>", Logger: "engine", Fields: json.RawMessage(`{"ms":900,"path":"/v/a\u0000b"}`)},
			{TS: "not a time", Level: "info", Event: "x"},
		})
		c.flushLogs(bg)
		c.flushMetrics(bg)
		if n := count(t, db, `SELECT count(*) FROM cluster_metrics_cache`); n != 3 {
			t.Fatalf("frames = %d, want 3 (slots :00, :10, :20)", n)
		}
		var worker, ip, node string
		if err := db.QueryRow(`SELECT worker_id, ip, node_id FROM cluster_metrics_cache LIMIT 1`).Scan(&worker, &ip, &node); err != nil ||
			worker != "studio_4242_ab12cd34" || ip != "fe80--1" || node != p.NodeID {
			t.Fatalf("frame row = %q %q %q %v", worker, ip, node, err)
		}
		if n := count(t, db, `SELECT count(*) FROM cluster_log_cache`); n != 2 {
			t.Fatalf("log rows = %d", n)
		}
		st := c.Snapshot()
		if st.Logs.Written != 2 || st.Logs.Dropped != 1 || st.Metrics.Written != 3 || st.Logs.Newest == nil ||
			!st.Logs.Newest.Equal(start.Add(2*time.Second)) {
			t.Fatalf("stats = %+v / %+v", st.Logs, st.Metrics)
		}
		// the same records again (a resend): kept once
		c.Logs(p, []agent.LogRecord{{TS: "2026-10-19T08:00:01.25Z", Level: "info", Event: "claimed", Logger: "engine",
			Fields: json.RawMessage(`{ "job" : 1 }`)}})
		c.flushLogs(bg)
		if n := count(t, db, `SELECT count(*) FROM cluster_log_cache`); n != 2 {
			t.Fatalf("a resent record was kept twice (%d rows)", n)
		}

		off := newTestCache(db, driver, clk, 0)
		off.Metrics(p, frame(start, 1), map[string]float64{"cpu.pct": 5}, false)
		off.Logs(p, []agent.LogRecord{{TS: "2026-10-19T09:00:00Z", Level: "info", Event: "late"}})
		off.flushLogs(bg)
		off.flushMetrics(bg)
		if count(t, db, `SELECT count(*) FROM cluster_log_cache`) != 2 || count(t, db, `SELECT count(*) FROM cluster_metrics_cache`) != 3 {
			t.Fatal("window 0 wrote cache rows")
		}
		if count(t, db, `SELECT count(*) FROM health_samples WHERE metric = 'cpu.pct' AND host = 'studio'`) != 1 {
			t.Fatal("window 0 must still write health_samples")
		}
		if off.Covers(KindLogs, start, start.Add(time.Hour)) {
			t.Fatal("window 0 covers nothing")
		}
	})
}

func TestFileNameIsThePipelinesSafeName(t *testing.T) {
	// expected values from the pipeline's safe_name (cluster/agent/files.py)
	for in, want := range map[string]string{
		"node-1a2b3c4d-4242":     "node-1a2b3c4d-4242",
		"studio/4242/ab12cd34":   "studio_4242_ab12cd34",
		"._x._":                  "x",
		"":                       "worker",
		"...":                    "worker",
		"مكتبة/1":                "1", // one "_" per character, then "._" trimmed
		strings.Repeat("a", 130): strings.Repeat("a", 120),
		"host.local/99/ffffffff": "host.local_99_ffffffff",
		"_lead":                  "lead",
		"-dash":                  "-dash",
		"a b:c":                  "a_b_c",
		"ends.with.dot.":         "ends.with.dot",
		"x\x00y":                 "x_y",
		"twelve/مك/twelve":       "twelve____twelve",
	} {
		if got := FileName(in); got != want {
			t.Errorf("FileName(%q) = %q, want %q", in, got, want)
		}
	}
}
