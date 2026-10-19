package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gunzip(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readManifestFile(t *testing.T, p string) manifest {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func modeOf(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

type archFixture struct {
	share, arch string
	clk         *clock
	a           *Archiver
}

func newArchFixture(t *testing.T, cfg ArchiveConfig) *archFixture {
	t.Helper()
	base := t.TempDir()
	f := &archFixture{share: filepath.Join(base, "share"), arch: filepath.Join(base, "archive"),
		clk: &clock{t: time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)}}
	_ = os.MkdirAll(f.share, 0o755)
	if cfg.Dir == "" {
		cfg.Dir = f.arch
	}
	f.a = NewArchiver(cfg, func() string { return f.share }, f.clk.Now, quiet())
	return f
}

func (f *archFixture) put(t *testing.T, rel string, lines ...string) {
	t.Helper()
	writeJSONL(t, filepath.Join(f.share, rel), lines...)
}

func TestArchiveADay(t *testing.T) {
	old := setUmask(0o022)
	defer setUmask(old)
	f := newArchFixture(t, ArchiveConfig{})
	f.put(t, "10.0.0.5/logs/2026-10-19/w1.jsonl", `{"ts":"2026-10-19T10:00:00Z","event":"a"}`)
	f.put(t, "10.0.0.5/logs/2026-10-19/w2.jsonl", `{"ts":"2026-10-19T10:00:01Z","event":"b"}`)
	f.put(t, "10.0.0.5/metrics/2026-10-19/w1.jsonl", `{"ts":"2026-10-19T10:00:00Z"}`) // not archived
	f.put(t, "10.0.0.5/logs/2026-10-21/w1.jsonl", `{"ts":"2026-10-21T00:30:00Z"}`)    // today: open
	f.put(t, "192.168.1.2/events/2026-10-20/hub.jsonl", `{"ts":"2026-10-20T10:00:00Z","event":"connect"}`)
	f.a.Pass(context.Background())

	day := filepath.Join(f.arch, "10.0.0.5", "logs", "2026-10-19")
	if got := gunzip(t, filepath.Join(day, "w1.jsonl.gz")); got != `{"ts":"2026-10-19T10:00:00Z","event":"a"}`+"\n" {
		t.Fatalf("w1 = %q", got)
	}
	if got := gunzip(t, filepath.Join(f.arch, "192.168.1.2", "events", "2026-10-20", "hub.jsonl.gz")); !strings.Contains(got, "connect") {
		t.Fatalf("hub = %q", got)
	}
	m := readManifestFile(t, filepath.Join(day, manifestName))
	if len(m.Files) != 2 || m.Files["w1.jsonl"] != int64(len(`{"ts":"2026-10-19T10:00:00Z","event":"a"}`)+1) {
		t.Fatalf("manifest = %+v", m)
	}
	for _, p := range []string{filepath.Join(f.arch, "10.0.0.5", "metrics"), filepath.Join(f.arch, "10.0.0.5", "logs", "2026-10-21")} {
		if exists(p) {
			t.Fatalf("%s archived", p)
		}
	}
	// modes: directories 0750, files 0640
	for _, p := range []string{filepath.Join(f.arch, "10.0.0.5"), filepath.Join(f.arch, "10.0.0.5", "logs"), day} {
		if modeOf(t, p) != 0o750 {
			t.Errorf("%s = %v", p, modeOf(t, p))
		}
	}
	for _, p := range []string{filepath.Join(day, "w1.jsonl.gz"), filepath.Join(day, manifestName)} {
		if modeOf(t, p) != 0o640 {
			t.Errorf("%s = %v", p, modeOf(t, p))
		}
	}
	s := f.a.Stats()
	if s.LastRun == nil || s.LastError != nil || s.DaysArchived != 2 || s.FilesArchived != 3 || s.Files != 3 ||
		s.DaysHeld != 2 || s.OldestDay == nil || *s.OldestDay != "2026-10-19" || s.SizeBytes == 0 || s.BytesArchived == 0 {
		t.Fatalf("stats = %+v", s)
	}

	// nothing new: nothing archived
	f.a.Pass(context.Background())
	if s := f.a.Stats(); s.FilesArchived != 0 || s.Files != 3 {
		t.Fatalf("second pass = %+v", s)
	}
	// a late record makes w1 grow: only w1 is archived again, whole
	f.put(t, "10.0.0.5/logs/2026-10-19/w1.jsonl", `{"ts":"2026-10-19T10:00:00Z","event":"a"}`, `{"ts":"2026-10-19T23:59:59Z","event":"late"}`)
	f.a.Pass(context.Background())
	if s := f.a.Stats(); s.FilesArchived != 1 {
		t.Fatalf("grown pass = %+v", s)
	}
	if got := gunzip(t, filepath.Join(day, "w1.jsonl.gz")); !strings.Contains(got, "late") || !strings.Contains(got, `"a"`) {
		t.Fatalf("w1 after growth = %q", got)
	}
	if m := readManifestFile(t, filepath.Join(day, manifestName)); m.Files["w1.jsonl"] <= int64(len(`{"ts":"2026-10-19T10:00:00Z","event":"a"}`)+1) {
		t.Fatalf("manifest after growth = %+v", m)
	}
	// once the day closes, today's folder is archived too
	f.clk.Set(time.Date(2026, 10, 22, 0, 30, 0, 0, time.UTC))
	f.a.Pass(context.Background())
	if !exists(filepath.Join(f.arch, "10.0.0.5", "logs", "2026-10-21", "w1.jsonl.gz")) {
		t.Fatal("a closed day was not archived")
	}
	// no temporary files are left
	_ = filepath.WalkDir(f.arch, func(p string, _ os.DirEntry, _ error) error {
		if strings.HasPrefix(filepath.Base(p), tmpPrefix) {
			t.Errorf("left %s", p)
		}
		return nil
	})
}

func TestArchiveManifestTornOrAbsent(t *testing.T) {
	f := newArchFixture(t, ArchiveConfig{})
	f.put(t, "10.0.0.5/jobs/2026-10-19/w1.jsonl", `{"ts":"2026-10-19T10:00:00Z","event":"done"}`)
	f.put(t, "10.0.0.5/jobs/2026-10-19/w2.jsonl", `{"ts":"2026-10-19T10:00:00Z","event":"done"}`)
	f.a.Pass(context.Background())
	day := filepath.Join(f.arch, "10.0.0.5", "jobs", "2026-10-19")
	// a torn manifest: the day is archived again
	if err := os.WriteFile(filepath.Join(day, manifestName), []byte(`{"files":{"w1.js`), 0o640); err != nil {
		t.Fatal(err)
	}
	f.a.Pass(context.Background())
	if s := f.a.Stats(); s.FilesArchived != 2 || s.LastError != nil {
		t.Fatalf("torn = %+v", s)
	}
	if m := readManifestFile(t, filepath.Join(day, manifestName)); len(m.Files) != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	// no manifest at all: same
	_ = os.Remove(filepath.Join(day, manifestName))
	f.a.Pass(context.Background())
	if s := f.a.Stats(); s.FilesArchived != 2 {
		t.Fatalf("absent = %+v", s)
	}
	// a manifest that lists a file whose archive copy is gone: archived again
	_ = os.Remove(filepath.Join(day, "w2.jsonl.gz"))
	f.a.Pass(context.Background())
	if s := f.a.Stats(); s.FilesArchived != 1 || !exists(filepath.Join(day, "w2.jsonl.gz")) {
		t.Fatalf("missing copy = %+v", s)
	}
}

func TestArchiveRefusesLinks(t *testing.T) {
	f := newArchFixture(t, ArchiveConfig{})
	outside := filepath.Join(filepath.Dir(f.share), "outside")
	writeJSONL(t, filepath.Join(outside, "secret.jsonl"), `{"ts":"2026-10-19T10:00:00Z","event":"secret"}`)
	_ = os.MkdirAll(filepath.Join(outside, "2026-10-18"), 0o755)
	writeJSONL(t, filepath.Join(outside, "2026-10-18", "x.jsonl"), `{"event":"secret"}`)
	// share side: a linked file and a linked day folder are not archived
	symlinkOrSkip(t, filepath.Join(outside, "secret.jsonl"), filepath.Join(f.share, "10.0.0.5", "logs", "2026-10-19", "w1.jsonl"))
	symlinkOrSkip(t, filepath.Join(outside, "2026-10-18"), filepath.Join(f.share, "10.0.0.5", "logs", "2026-10-18"))
	f.put(t, "10.0.0.5/logs/2026-10-19/w2.jsonl", `{"ts":"2026-10-19T10:00:00Z","event":"fine"}`)
	// archive side: a planted link where a day folder or a file goes
	f.put(t, "10.0.0.6/logs/2026-10-19/w1.jsonl", `{"event":"a"}`)
	f.put(t, "10.0.0.7/logs/2026-10-19/w1.jsonl", `{"event":"b"}`)
	symlinkOrSkip(t, outside, filepath.Join(f.arch, "10.0.0.6", "logs", "2026-10-19"))
	victim := filepath.Join(outside, "victim")
	_ = os.WriteFile(victim, []byte("keep"), 0o600)
	symlinkOrSkip(t, victim, filepath.Join(f.arch, "10.0.0.7", "logs", "2026-10-19", "w1.jsonl.gz"))

	f.a.Pass(context.Background())
	got := map[string]bool{}
	_ = filepath.WalkDir(f.arch, func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.HasSuffix(p, gzSuffix) {
			got[strings.TrimPrefix(p, f.arch+string(filepath.Separator))] = true
		}
		return nil
	})
	if !got[filepath.Join("10.0.0.5", "logs", "2026-10-19", "w2.jsonl.gz")] ||
		got[filepath.Join("10.0.0.5", "logs", "2026-10-19", "w1.jsonl.gz")] ||
		got[filepath.Join("10.0.0.5", "logs", "2026-10-18", "x.jsonl.gz")] {
		t.Fatalf("archived = %v", got)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("wrote through a link in the archive")
	}
	entries, _ := os.ReadDir(outside)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".gz") || e.Name() == manifestName || strings.HasPrefix(e.Name(), tmpPrefix) {
			t.Fatalf("wrote outside the archive: %s", e.Name())
		}
	}
	if s := f.a.Stats(); s.LastError == nil {
		t.Fatal("the refused writes were not reported")
	}
}

func TestArchiveRetention(t *testing.T) {
	f := newArchFixture(t, ArchiveConfig{Days: 30})
	mk := func(ip, kind, day string, size int) {
		p := filepath.Join(f.arch, ip, kind, day, "w.jsonl.gz")
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		_ = os.WriteFile(p, bytes.Repeat([]byte("x"), size), 0o640)
	}
	mk("10.0.0.5", "logs", "2026-08-01", 100) // past 30 days
	mk("10.0.0.6", "jobs", "2026-08-01", 100)
	mk("10.0.0.5", "logs", "2026-10-01", 100)
	mk("10.0.0.6", "logs", "2026-10-01", 100)
	mk("10.0.0.5", "logs", "2026-10-02", 100)
	mk("10.0.0.5", "logs", "2026-10-03", 100)
	f.a.Pass(context.Background())
	if exists(filepath.Join(f.arch, "10.0.0.5", "logs", "2026-08-01")) || exists(filepath.Join(f.arch, "10.0.0.6", "jobs", "2026-08-01")) {
		t.Fatal("days past the age limit kept")
	}
	if s := f.a.Stats(); s.DaysHeld != 3 || s.Files != 4 || *s.OldestDay != "2026-10-01" || s.SizeBytes != 400 {
		t.Fatalf("stats = %+v", s)
	}
	// by size: whole days, oldest first (both machines' 2026-10-01 go together)
	f.a.cfg.MaxBytes = 250
	f.a.mu.Lock()
	f.a.lastRetention = time.Time{}
	f.a.mu.Unlock()
	f.a.Pass(context.Background())
	if exists(filepath.Join(f.arch, "10.0.0.5", "logs", "2026-10-01")) || exists(filepath.Join(f.arch, "10.0.0.6", "logs", "2026-10-01")) ||
		!exists(filepath.Join(f.arch, "10.0.0.5", "logs", "2026-10-02")) || !exists(filepath.Join(f.arch, "10.0.0.5", "logs", "2026-10-03")) {
		t.Fatal("size retention")
	}
	// retention runs once a day
	mk("10.0.0.5", "logs", "2026-08-02", 100)
	f.a.Pass(context.Background())
	if !exists(filepath.Join(f.arch, "10.0.0.5", "logs", "2026-08-02")) {
		t.Fatal("retention ran twice in a day")
	}
	f.clk.Set(f.clk.Now().Add(25 * time.Hour))
	f.a.Pass(context.Background())
	if exists(filepath.Join(f.arch, "10.0.0.5", "logs", "2026-08-02")) {
		t.Fatal("retention did not run the next day")
	}
	// 0: no age limit
	g := newArchFixture(t, ArchiveConfig{Days: 0})
	p := filepath.Join(g.arch, "10.0.0.5", "logs", "2020-01-01", "w.jsonl.gz")
	_ = os.MkdirAll(filepath.Dir(p), 0o750)
	_ = os.WriteFile(p, []byte("x"), 0o640)
	g.a.Pass(context.Background())
	if !exists(p) {
		t.Fatal("Days=0 removed an old day")
	}
}

func TestArchiveFailureDoesNotBlock(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "file")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	f := newFixture(t, func(c *Config) {
		c.Archive = &ArchiveConfig{Dir: filepath.Join(blocker, "archive"), Kinds: DefaultArchiveKinds, Days: 365}
	})
	writeJSONL(t, filepath.Join(f.dir, "10.0.0.5", "logs", "2026-10-01", "w1.jsonl"), `{"event":"x"}`)
	waitFor(t, "a failed pass", func() bool { s := f.hub.archiver.Stats(); return s.LastRun != nil })
	f.hub.archiver.Pass(context.Background())
	if s := f.hub.archiver.Stats(); s.LastError == nil {
		t.Fatalf("stats = %+v", s)
	}
	// the hub goes on
	if _, err := f.dialAgent(helloOpts{kind: "home", homeKey: f.homeKey}); err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	decode(t, f.do("GET", "/api/cluster/processes", "", "admin"), &snap)
	if a, _ := snap["archive"].(map[string]any); a["last_error"] == nil || a["last_run"] == nil {
		t.Fatalf("archive stats = %v", snap["archive"])
	}
}

// A day read from the archive answers like the same day read from the
// share; the files index lists the archive's days.
func TestHistoryFromTheArchive(t *testing.T) {
	arch := filepath.Join(t.TempDir(), "archive")
	f := newFixture(t, func(c *Config) {
		c.Archive = &ArchiveConfig{Dir: arch, Kinds: DefaultArchiveKinds, Days: 0}
	})
	const old = "2026-10-01"
	ip := filepath.Join(f.dir, "192.168.50.31")
	writeJSONL(t, filepath.Join(ip, "logs", old, "w1.jsonl"),
		`{"ts":"2026-10-01T10:00:00Z","level":"info","event":"start"}`,
		`{"ts":"2026-10-01T10:00:02Z","level":"error","event":"boom"}`)
	writeJSONL(t, filepath.Join(ip, "logs", old, "w2.jsonl"), `{"ts":"2026-10-01T10:00:01Z","level":"warning","event":"slow"}`)
	writeJSONL(t, filepath.Join(ip, "jobs", old, "w1.jsonl"), `{"ts":"2026-10-01T10:00:00Z","event":"done","stage":"probe"}`)
	writeJSONL(t, filepath.Join(f.dir, testHomeIP, "events", old, "hub.jsonl"),
		`{"ts":"2026-10-01T09:00:00Z","event":"connect","ip":"192.168.50.31"}`)
	f.hub.archiver.Pass(context.Background())

	paths := []string{"/logs?date=" + old, "/logs?date=" + old + "&level=warning", "/jobs?date=" + old, "/events?date=" + old}
	fromShare := map[string]string{}
	for _, p := range paths {
		resp := f.do("GET", "/api/cluster/files/192.168.50.31"+p, "", "admin")
		b, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("X-Maktaba-Source") != "files" {
			t.Fatalf("%s from %q", p, resp.Header.Get("X-Maktaba-Source"))
		}
		fromShare[p] = string(b)
	}
	// the share's retention removes the day
	_ = os.RemoveAll(filepath.Join(ip, "logs", old))
	_ = os.RemoveAll(filepath.Join(ip, "jobs", old))
	_ = os.RemoveAll(filepath.Join(f.dir, testHomeIP, "events", old))
	for _, p := range paths {
		resp := f.do("GET", "/api/cluster/files/192.168.50.31"+p, "", "admin")
		b, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("X-Maktaba-Source") != "archive" || string(b) != fromShare[p] {
			t.Fatalf("%s from %q:\n%s\nwant\n%s", p, resp.Header.Get("X-Maktaba-Source"), b, fromShare[p])
		}
	}
	if !strings.Contains(fromShare["/logs?date="+old], `"worker_id":"w2"`) || !strings.Contains(fromShare["/events?date="+old], "connect") {
		t.Fatalf("answers = %v", fromShare)
	}

	// the index: the share has no days left for these kinds, the archive has
	var idx struct {
		Nodes []MachineFiles `json:"nodes"`
	}
	decode(t, f.do("GET", "/api/cluster/files", "", "admin"), &idx)
	var node *MachineFiles
	for i := range idx.Nodes {
		if idx.Nodes[i].IP == "192.168.50.31" {
			node = &idx.Nodes[i]
		}
	}
	if node == nil || len(node.Dates["logs"]) != 0 || len(node.ArchivedDates["logs"]) != 1 || node.ArchivedDates["logs"][0] != old ||
		len(node.ArchivedDates["jobs"]) != 1 || node.ArchivedDates["metrics"] == nil {
		t.Fatalf("index = %+v", idx.Nodes)
	}
	// a machine whose folder is gone from the share is still listed
	_ = os.RemoveAll(ip)
	decode(t, f.do("GET", "/api/cluster/files", "", "admin"), &idx)
	found := false
	for _, n := range idx.Nodes {
		if n.IP == "192.168.50.31" && len(n.ArchivedDates["logs"]) == 1 && len(n.Processes) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("archive-only machine missing: %+v", idx.Nodes)
	}
}

func TestArchiveEnv(t *testing.T) {
	cfg, warn := FromEnv(func(k string) string {
		return map[string]string{"MAKTABA_ARCHIVE_DIR": "/srv/archive", "MAKTABA_ARCHIVE_KINDS": "logs, events,bogus",
			"MAKTABA_ARCHIVE_DAYS": "0", "MAKTABA_ARCHIVE_MAX_GB": "5"}[k]
	})
	a := cfg.Archive
	if a == nil || a.Dir != "/srv/archive" || strings.Join(a.Kinds, ",") != "logs,events" || a.Days != 0 || a.MaxBytes != 5<<30 ||
		len(warn) != 1 || !strings.Contains(warn[0], "bogus") {
		t.Fatalf("archive = %+v %v", a, warn)
	}
	cfg, _ = FromEnv(func(string) string { return "" })
	a = cfg.Archive
	if a == nil || a.Dir != DefaultArchiveDir() || strings.Join(a.Kinds, ",") != "logs,jobs,llm,events" || a.Days != 365 || a.MaxBytes != 20<<30 {
		t.Fatalf("defaults = %+v", a)
	}
}

// macOS writes a "._<name>" sidecar beside every file on a share that
// cannot keep extended attributes: history never reads one as a process's
// records, and the archive never copies one.
func TestMacSidecarsAreIgnored(t *testing.T) {
	f := newArchFixture(t, ArchiveConfig{})
	f.put(t, "10.0.0.5/logs/2026-10-19/w1.jsonl", `{"ts":"2026-10-19T10:00:00Z","event":"a"}`)
	side := filepath.Join(f.share, "10.0.0.5", "logs", "2026-10-19", "._w1.jsonl")
	if err := os.WriteFile(side, []byte("\x00\x05\x16\x07Mac OS X        \x00\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(f.share)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	files := dayFiles(root, "10.0.0.5", "logs", "2026-10-19")
	if len(files) != 1 || files[0].workerID != "w1" {
		t.Fatalf("day files = %+v", files)
	}
	f.a.Pass(context.Background())
	day := filepath.Join(f.arch, "10.0.0.5", "logs", "2026-10-19")
	if exists(filepath.Join(day, "._w1.jsonl.gz")) {
		t.Fatal("a sidecar was archived")
	}
	if m := readManifestFile(t, filepath.Join(day, manifestName)); len(m.Files) != 1 {
		t.Fatalf("manifest = %+v", m.Files)
	}
}
