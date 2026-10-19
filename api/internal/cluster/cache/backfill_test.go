package cache

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

func writeFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func logLineJSON(ts, level, event string, fields string) string {
	return `{"ts": "` + ts + `", "level": "` + level + `", "event": "` + event + `", "logger": "engine", "fields": ` + fields + "}\n"
}

func metricsLine(ts string, cpu int) string {
	b, _ := json.Marshal(map[string]any{"ts": ts, "system": map[string]any{"cpu_pct": cpu}, "health": map[string]any{"cpu.pct": cpu}})
	return string(b) + "\n"
}

// clusterTree is a cluster directory with every case a pass meets: two
// machines, two days, a record older than the window, a corrupt line, a
// write in progress, and links (to a folder, a day, a file) and names
// that must not be followed or read.
func clusterTree(t *testing.T) (dir, outside string) {
	t.Helper()
	base := t.TempDir()
	dir, outside = filepath.Join(base, "cluster"), filepath.Join(base, "outside")
	secret := logLineJSON("2026-10-19T09:00:00Z", "error", "SECRET", `{}`)
	writeFile(t, filepath.Join(outside, "secret.jsonl"), secret)
	writeFile(t, filepath.Join(outside, "logs", "2026-10-19", "w-x.jsonl"), secret)
	writeFile(t, filepath.Join(outside, "2026-10-19", "w-x.jsonl"), secret)

	m := filepath.Join(dir, "192.168.50.31")
	writeFile(t, filepath.Join(m, "logs", "2026-10-19", "w-a.jsonl"),
		logLineJSON("2026-10-19T08:00:01.250Z", "info", "claimed", `{"job": 1}`),
		"{not json at all\n",
		logLineJSON("2026-10-19T08:00:02Z", "warning", "slow read", `{"ms": 900, "path": "/v/a.mkv"}`),
		logLineJSON("2026-10-19T08:00:03Z", "error", "boom", `{"cause": "nas_timeout"}`),
		`{"ts": "2026-10-19T08:00:04Z", "level": "info", "ev`) // a write in progress
	writeFile(t, filepath.Join(m, "logs", "2026-10-18", "w-a.jsonl"),
		logLineJSON("2026-10-18T09:00:00Z", "info", "too old", `{}`),
		logLineJSON("2026-10-18T20:00:00Z", "debug", "yesterday", `{"n": 2}`))
	var frames []string
	for s := 0; s < 30; s += 2 {
		frames = append(frames, metricsLine(time.Date(2026, 10, 19, 8, 0, s, 0, time.UTC).Format(time.RFC3339), s))
	}
	writeFile(t, filepath.Join(m, "metrics", "2026-10-19", "w-a.jsonl"), frames...)
	writeFile(t, filepath.Join(m, "logs", "2026-10-19", "bad name!.jsonl"), secret)
	writeFile(t, filepath.Join(m, "logs", "2026-10-19", "notes.txt"), secret)
	writeFile(t, filepath.Join(m, "logs", "2026-10-30", "w-a.jsonl"), secret) // past the window's end
	must(t, os.Symlink(filepath.Join(outside, "secret.jsonl"), filepath.Join(m, "logs", "2026-10-19", "w-link.jsonl")))

	writeFile(t, filepath.Join(dir, "fe80--1", "logs", "2026-10-19", "w-6.jsonl"),
		logLineJSON("2026-10-19T07:00:00Z", "critical", "v6", `{}`))
	must(t, os.Symlink(outside, filepath.Join(dir, "10.9.9.9")))
	must(t, os.MkdirAll(filepath.Join(dir, "192.168.50.40"), 0o755))
	must(t, os.Symlink(filepath.Join(outside, "logs"), filepath.Join(dir, "192.168.50.40", "logs")))
	must(t, os.MkdirAll(filepath.Join(dir, "192.168.50.41", "logs"), 0o755))
	must(t, os.Symlink(filepath.Join(outside, "2026-10-19"), filepath.Join(dir, "192.168.50.41", "logs", "2026-10-19")))
	writeFile(t, filepath.Join(dir, "..bad", "logs", "2026-10-19", "w-a.jsonl"), secret)
	writeFile(t, filepath.Join(dir, "README.txt"), "not a folder\n")
	return dir, outside
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func events(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT event FROM cluster_log_cache ORDER BY ts, event`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var e string
		must(t, rows.Scan(&e))
		out = append(out, e)
	}
	return out
}

// A pass reads the window's records from the files (never through a link,
// never a name outside the patterns), skips a corrupt line, leaves a
// write in progress for later, and covers the window; passes are
// idempotent and read only what the files gained; a record from the
// channel and the same record in its file are kept once; a restart reads
// on from where it stopped, and emptied tables are refilled whole.
func TestBackfillFromTheFiles(t *testing.T) {
	eachDB(t, func(t *testing.T, db *sql.DB, driver string) {
		dir, _ := clusterTree(t)
		clk := newClock("2026-10-19T12:00:00Z")
		c := newTestCache(db, driver, clk, 24*time.Hour)
		now := clk.Now()
		if c.Covers(KindLogs, now.Add(-time.Hour), now) {
			t.Fatal("covered before any pass")
		}
		must(t, c.Backfill(bg, dir))
		want := []string{"yesterday", "v6", "claimed", "slow read", "boom"}
		if got := events(t, db); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("events = %v, want %v", got, want)
		}
		if n := count(t, db, `SELECT count(*) FROM cluster_metrics_cache`); n != 3 {
			t.Fatalf("frames = %d, want 3 (one per 10 s slot)", n)
		}
		for _, k := range []string{KindLogs, KindMetrics} {
			if !c.Covers(k, now.Add(-24*time.Hour), now.Add(time.Hour)) {
				t.Fatalf("%s not covered after the pass", k)
			}
			if c.Covers(k, now.Add(-25*time.Hour), now) {
				t.Fatalf("%s covered before the window", k)
			}
		}
		if c.Covers("jobs", now.Add(-time.Hour), now) {
			t.Fatal("jobs are never cached")
		}
		positions := count(t, db, `SELECT count(*) FROM cluster_cache_files`)
		if positions != 4 { // w-a logs × 2 days, w-a metrics, w-6 logs
			t.Fatalf("read positions = %d", positions)
		}

		// again: nothing new
		before := c.Snapshot()
		must(t, c.Backfill(bg, dir))
		if after := c.Snapshot(); after.Logs.FromFiles != before.Logs.FromFiles || len(events(t, db)) != 5 {
			t.Fatalf("a second pass added rows: %+v", after.Logs)
		}

		// the write finishes, a record follows; the channel sends one of them first
		today := filepath.Join(dir, "192.168.50.31", "logs", "2026-10-19", "w-a.jsonl")
		appendFile(t, today, `ent": "finished", "logger": "engine", "fields": {}}`+"\n"+
			logLineJSON("2026-10-19T08:00:05Z", "info", "next", `{"b": [1, 2.50], "a": "x"}`))
		c.Logs(agent.Proc{WorkerID: "w-a", IP: "192.168.50.31"}, []agent.LogRecord{
			{TS: "2026-10-19T08:00:05.000Z", Level: "info", Event: "next", Logger: "engine", Fields: json.RawMessage(`{"a":"x","b":[1,2.50]}`)},
			{TS: "2026-10-19T08:00:06Z", Level: "info", Event: "channel only", Logger: "engine"},
		})
		c.flushLogs(bg)
		clk.Add(time.Minute)
		must(t, c.Backfill(bg, dir))
		want = []string{"yesterday", "v6", "claimed", "slow read", "boom", "finished", "next", "channel only"}
		if got := events(t, db); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("events = %v, want %v", got, want)
		}

		// a restart reads on from the stored positions
		r := newTestCache(db, driver, clk, 24*time.Hour)
		must(t, r.Backfill(bg, dir))
		if st := r.Snapshot(); st.Logs.FromFiles != 0 || st.Metrics.FromFiles != 0 || !r.Covers(KindLogs, now, now) {
			t.Fatalf("restart re-read the files: %+v", st)
		}

		// emptied tables (a crash empties UNLOGGED ones): the whole window again
		must2(t, db, `DELETE FROM cluster_log_cache`)
		must2(t, db, `DELETE FROM cluster_metrics_cache`)
		e := newTestCache(db, driver, clk, 24*time.Hour)
		must(t, e.Backfill(bg, dir))
		want = []string{"yesterday", "v6", "claimed", "slow read", "boom", "finished", "next"}
		if got := events(t, db); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("after emptying: events = %v, want %v", got, want)
		}
		if count(t, db, `SELECT count(*) FROM cluster_metrics_cache`) != 3 {
			t.Fatal("frames not refilled")
		}

		// the cache is only as fresh as its last pass
		clk.Add(e.staleAfter() + time.Second)
		if e.Covers(KindLogs, clk.Now().Add(-time.Hour), clk.Now()) {
			t.Fatal("covered with no recent pass")
		}
	})
}

func must2(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// A file that cannot be read leaves the window uncovered (the cache may
// not hold it); a missing directory is no error and covers nothing.
func TestBackfillIncompletePassCoversNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	db := sqliteDB(t)
	dir, _ := clusterTree(t)
	clk := newClock("2026-10-19T12:00:00Z")
	c := newTestCache(db, "sqlite", clk, 24*time.Hour)
	must(t, c.Backfill(bg, filepath.Join(dir, "missing")))
	if c.Covers(KindLogs, clk.Now(), clk.Now()) {
		t.Fatal("a missing directory covers")
	}
	locked := filepath.Join(dir, "192.168.50.31", "logs", "2026-10-19", "w-a.jsonl")
	must(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	must(t, c.Backfill(bg, dir))
	if c.Covers(KindLogs, clk.Now(), clk.Now()) {
		t.Fatal("covered with an unreadable file")
	}
	if !c.Covers(KindMetrics, clk.Now(), clk.Now()) {
		t.Fatal("metrics were read whole")
	}
}

// The passes run at a bounded rate (lines per second).
func TestBackfillRateIsBounded(t *testing.T) {
	db := sqliteDB(t)
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, logLineJSON(time.Date(2026, 10, 19, 8, 0, 0, i*1000, time.UTC).Format(time.RFC3339Nano), "info", "e", `{}`))
	}
	writeFile(t, filepath.Join(dir, "10.0.0.1", "logs", "2026-10-19", "w.jsonl"), lines...)
	clk := newClock("2026-10-19T12:00:00Z")
	c := New(db, Options{Window: 24 * time.Hour, Driver: "sqlite", Now: clk.Now, Logger: quietLogger(), ReadRate: 50})
	began := time.Now()
	must(t, c.Backfill(bg, dir))
	// a burst of 50 lines, then 10 more at 50 a second
	if took := time.Since(began); took < 150*time.Millisecond {
		t.Fatalf("60 lines at 50/s took %s", took)
	}
	if n := count(t, db, `SELECT count(*) FROM cluster_log_cache`); n != 60 {
		t.Fatalf("rows = %d", n)
	}
}
