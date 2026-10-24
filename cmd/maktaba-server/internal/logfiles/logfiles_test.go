package logfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// An update's story across the three files, cut to its window: each line's time from
// itself or the line before it, the files merged by time, the roles' JSON read for their
// level, service and message, the launcher's events in words.
func TestReadAWindowAcrossTheFiles(t *testing.T) {
	d := Dirs{Logs: t.TempDir(), Updates: t.TempDir()}
	write(t, filepath.Join(d.Logs, "server.log.1"), `{"ts":"2026-10-24T05:20:00Z","level":"INFO","service":"api","msg":"old"}
`)
	write(t, filepath.Join(d.Logs, "server.log"), `[serve] a line before any time
{"ts":"2026-10-24T05:29:59Z","level":"INFO","service":"api","msg":"listening"}
[serve] app: dumping the database before 0.3.1's migrations
{"ts":"2026-10-24T05:30:02.5Z","level":"ERROR","service":"api","msg":"migration 0150 failed"}
  a continuation line
[launcher] {"ts":"2026-10-24T05:30:03Z","event":"boot_loop","version":"0.3.1","detail":{"starts":3}}
time=2026-10-24T05:40:00Z level=WARN msg="after the window" k=v
`)
	write(t, filepath.Join(d.Logs, "app.log"), `2026-10-24T05:30:00Z update: 0.3.0 -> 0.3.1 started (badge)
2026-10-24T05:30:01Z update: downloaded and verified
2026-10-24T06:00:00Z update: later
`)
	write(t, filepath.Join(d.Updates, "launcher.log"), `{"ts":"2026-10-24T05:30:03Z","event":"rollback","version":"0.3.1","detail":{"reason":"boot_loop"}}
not json at all
`)
	r := Read(d, Query{Since: at("2026-10-24T05:30:00Z"), Until: at("2026-10-24T05:35:00Z")})
	var got []string
	for _, e := range r.Entries {
		got = append(got, e.File+"|"+e.Service+"|"+e.Level+"|"+e.Msg)
	}
	want := []string{
		"app.log|app||update: 0.3.0 -> 0.3.1 started (badge)",
		"app.log|app||update: downloaded and verified",
		"server.log|api|error|migration 0150 failed",
		"server.log|server||  a continuation line",
		"server.log|launcher||boot_loop 0.3.1 {\"starts\":3}",
		"launcher.log|launcher||rollback 0.3.1 {\"reason\":\"boot_loop\"}",
		"launcher.log|launcher||not json at all",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r.Entries[3].TS != "2026-10-24T05:30:02.5Z" {
		t.Errorf("a continuation line takes the time before it: %q", r.Entries[3].TS)
	}
	if r.Entries[2].Raw != `{"ts":"2026-10-24T05:30:02.5Z","level":"ERROR","service":"api","msg":"migration 0150 failed"}` {
		t.Errorf("raw: %q", r.Entries[2].Raw)
	}
	for _, f := range r.Files {
		if f.Missing || f.Truncated {
			t.Errorf("file: %+v", f)
		}
	}
	// with no window, every line; one with no time of its own takes the time before it,
	// across a rotation (server.log.1's last); one before any time has none
	all := Read(d, Query{})
	if len(all.Entries) != 13 || all.Entries[0].Msg != "old" || all.Entries[1].Msg != "a line before any time" ||
		all.Entries[1].TS != "2026-10-24T05:20:00Z" {
		t.Fatalf("no window: %d entries, first %+v %+v", len(all.Entries), all.Entries[0], all.Entries[1])
	}
	write(t, filepath.Join(d.Logs, "server.log.1"), "")
	untimed := Read(d, Query{Files: []string{"server"}})
	if untimed.Entries[0].TS != "" || untimed.Entries[0].Msg != "a line before any time" {
		t.Fatalf("a line before any time: %+v", untimed.Entries[0])
	}
	if w := Read(d, Query{Files: []string{"server"}, Since: at("2026-10-24T05:00:00Z")}); w.Entries[0].Msg == "a line before any time" {
		t.Fatal("a line with no time is in a window with a start")
	}
	// slog's text form
	found := false
	for _, e := range all.Entries {
		found = found || (e.Level == "warn" && e.Msg == "after the window" && e.TS == "2026-10-24T05:40:00Z")
	}
	if !found {
		t.Errorf("slog's text not read: %+v", all.Entries)
	}
}

// Only the files asked for; a file that is not there says so; the newest lines when
// there are more than the limit; the channel's answer fits its frame.
func TestReadLimitsAndMissing(t *testing.T) {
	d := Dirs{Logs: t.TempDir(), Updates: filepath.Join(t.TempDir(), "none")}
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString(time.Date(2026, 10, 24, 5, 0, i, 0, time.UTC).Format(time.RFC3339) + " line " + strings.Repeat("x", 100) + "\n")
	}
	write(t, filepath.Join(d.Logs, "app.log"), b.String())
	r := Read(d, Query{Files: []string{"app", "launcher"}, Limit: 10})
	if len(r.Entries) != 10 || !strings.HasPrefix(r.Entries[9].TS, "2026-10-24T05:00:49") {
		t.Fatalf("newest 10: %d, last %+v", len(r.Entries), r.Entries[len(r.Entries)-1])
	}
	if len(r.Files) != 2 || r.Files[0].Name != "app.log" || !r.Files[0].Truncated || !r.Files[1].Missing {
		t.Fatalf("files: %+v", r.Files)
	}
	fit := Fit(Read(d, Query{Files: []string{"app"}}), 2000)
	if len(fit.Entries) == 0 || len(fit.Entries) >= 50 || !fit.Files[0].Truncated ||
		!strings.HasPrefix(fit.Entries[len(fit.Entries)-1].TS, "2026-10-24T05:00:49") {
		t.Fatalf("fit: %d entries, %+v", len(fit.Entries), fit.Files)
	}
	long := strings.Repeat("é", 3000)
	write(t, filepath.Join(d.Logs, "server.log"), "2026-10-24T05:00:00Z "+long+"\n")
	s := Read(d, Query{Files: []string{"server"}})
	if len(s.Entries) != 1 || len(s.Entries[0].Raw) > maxLine+len("…") || !strings.HasSuffix(s.Entries[0].Msg, "…") {
		t.Fatalf("a long line is cut: %d", len(s.Entries[0].Raw))
	}
}

func TestParseQuery(t *testing.T) {
	q, err := ParseQuery(query{"files": "server,app", "since": "2026-10-24T05:30:00Z",
		"until": "2026-10-24T05:35:00.5+02:00", "limit": "50"}.get)
	if err != nil || len(q.Files) != 2 || q.Since.IsZero() || q.Until.IsZero() || q.Limit != 50 {
		t.Fatalf("%+v %v", q, err)
	}
	for _, bad := range []map[string]string{{"files": "server,../etc/passwd"}, {"since": "yesterday"}, {"limit": "-1"}} {
		if _, err := ParseQuery(query(bad).get); err == nil {
			t.Errorf("taken: %v", bad)
		}
	}
}

type query map[string]string

func (q query) get(k string) string { return q[k] }
