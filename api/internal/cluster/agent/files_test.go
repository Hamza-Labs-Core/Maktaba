package agent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

func generateKeyPair(t *testing.T) ([]byte, []byte, error) {
	t.Helper()
	priv, pub, err := cluster.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub, nil
}

func nodeRow(id string, pub []byte, exp time.Time) cluster.Node {
	return cluster.Node{ID: id, Name: "n", NodePubkey: cluster.EncodeKey(pub), ExpiresAt: &exp, CreatedAt: time.Now()}
}

func eventsPath(f *fixture) string {
	return filepath.Join(f.dir, testHomeIP, KindEvents, time.Now().UTC().Format("2006-01-02"), hubFile)
}

func readLines(t *testing.T, p string) []string {
	t.Helper()
	fh, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer func() { _ = fh.Close() }()
	var out []string
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func countLines(t *testing.T, p string) int { return len(readLines(t, p)) }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestFileWriterLayoutRotationAndCap(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: time.Date(2026, 10, 19, 23, 59, 0, 0, time.UTC)}
	fw := NewFileWriter(16, 0, clk.Now, quiet())
	fw.SetTarget(dir, IPDir("fe80::1"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); fw.Run(ctx) }()

	fw.Write(KindEvents, map[string]any{"event": "a"})
	fw.Write(KindQueues, map[string]any{"q": 1})
	clk.Set(time.Date(2026, 10, 20, 0, 0, 1, 0, time.UTC))
	fw.Write(KindEvents, map[string]any{"event": "b"})
	waitFor(t, "written", func() bool {
		fw.Flush()
		return countLines(t, filepath.Join(dir, "fe80--1", KindEvents, "2026-10-20", hubFile)) == 1
	})
	if n := countLines(t, filepath.Join(dir, "fe80--1", KindEvents, "2026-10-19", hubFile)); n != 1 {
		t.Fatalf("day 1 events = %d", n)
	}
	if n := countLines(t, filepath.Join(dir, "fe80--1", KindQueues, "2026-10-19", hubFile)); n != 1 {
		t.Fatalf("queues = %d", n)
	}

	cancel()
	<-done

	// the cap: past it records are counted, not written
	capped := NewFileWriter(16, 20, clk.Now, quiet())
	capped.SetTarget(dir, "h")
	for i := 0; i < 5; i++ {
		capped.Write(KindLibrary, map[string]any{"i": i}) // 8 bytes a line
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { defer close(done2); capped.Run(ctx2) }()
	waitFor(t, "capped", func() bool { return capped.Stats().Capped == 3 })
	cancel2()
	<-done2
	if n := countLines(t, filepath.Join(dir, "h", KindLibrary, "2026-10-20", hubFile)); n != 2 {
		t.Fatalf("library lines under the cap = %d", n)
	}
}

func TestFileWriterNeverBlocks(t *testing.T) {
	fw := NewFileWriter(2, 0, time.Now, quiet()) // nobody runs it
	fw.SetTarget(t.TempDir(), "h")
	start := time.Now()
	for i := 0; i < 100; i++ {
		fw.Write(KindEvents, map[string]any{"i": i})
	}
	if time.Since(start) > time.Second {
		t.Fatal("Write blocked")
	}
	if fw.Stats().Dropped != 98 {
		t.Fatalf("dropped = %d", fw.Stats().Dropped)
	}
}

func TestFileWriterCountsDiskErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	fw := NewFileWriter(4, 0, time.Now, quiet())
	fw.SetTarget(blocker, "h") // a file where a directory must be
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fw.Run(ctx)
	fw.Write(KindEvents, map[string]any{"x": 1})
	waitFor(t, "error counted", func() bool { return fw.Stats().Errors > 0 })
}

func mkday(t *testing.T, dir, ip, kind, date, name string, mod time.Time) {
	t.Helper()
	p := filepath.Join(dir, ip, kind, date)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	fp := filepath.Join(p, name)
	if err := os.WriteFile(fp, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(fp, mod, mod)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSweeperRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -20)
	// the home: its hub files expire; a home process's own files are its own
	mkday(t, dir, "192.168.50.1", KindEvents, "2026-10-01", hubFile, old)
	mkday(t, dir, "192.168.50.1", KindEvents, "2026-10-29", hubFile, now)
	mkday(t, dir, "192.168.50.1", "logs", "2026-10-01", "w1.jsonl", old)
	// a machine gone for good: everything past retention goes
	mkday(t, dir, "192.168.50.7", "logs", "2026-10-01", "w7.jsonl", old)
	mkday(t, dir, "192.168.50.7", "metrics", "2026-10-02", "w7.jsonl", old)
	// a machine with a live session keeps its files (it cleans its own)
	mkday(t, dir, "192.168.50.8", "logs", "2026-10-01", "w8.jsonl", old)
	// a machine that wrote recently but is not connected now: kept
	mkday(t, dir, "192.168.50.9", "logs", "2026-10-01", "w9.jsonl", old)
	mkday(t, dir, "192.168.50.9", "logs", "2026-10-29", "w9.jsonl", now.Add(-time.Hour))

	s := &Sweeper{Days: 14, Now: func() time.Time { return now }, LiveIPs: func() map[string]bool { return map[string]bool{"192.168.50.8": true} }}
	if _, err := s.Sweep(dir, "192.168.50.1"); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		"192.168.50.1/events/2026-10-01":           false,
		"192.168.50.1/events/2026-10-29/hub.jsonl": true,
		"192.168.50.1/logs/2026-10-01/w1.jsonl":    true,
		"192.168.50.7/logs/2026-10-01":             false,
		"192.168.50.7/metrics/2026-10-02":          false,
		"192.168.50.8/logs/2026-10-01/w8.jsonl":    true,
		"192.168.50.9/logs/2026-10-01/w9.jsonl":    true,
	} {
		if got := exists(filepath.Join(dir, p)); got != want {
			t.Errorf("%s exists = %v, want %v", p, got, want)
		}
	}
}

func TestPathMap(t *testing.T) {
	m, err := ParsePathMap(`Y:/=/Volumes/Y;O:\=/Volumes/O/;\\nas\share=/mnt/share`)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"Y:/Channel/a.mp4":    "/Volumes/Y/Channel/a.mp4",
		"y:/Channel/a.mp4":    "/Volumes/Y/Channel/a.mp4",
		`O:\Movies\b.mkv`:     "/Volumes/O/Movies/b.mkv",
		`\\NAS\share\maktaba`: "/mnt/share/maktaba",
		"/already/local/x":    "/already/local/x",
		"Z:/unmapped/z":       "Z:/unmapped/z",
	} {
		if got := m.ToLocal(in); got != want {
			t.Errorf("ToLocal(%q) = %q, want %q", in, got, want)
		}
	}
	if got := m.ToDB("/Volumes/Y/Channel/a.mp4"); got != "Y:/Channel/a.mp4" {
		t.Errorf("ToDB = %q", got)
	}
	if got := m.ToDB("/mnt/share/x"); got != "//nas/share/x" {
		t.Errorf("ToDB UNC = %q", got)
	}
	// longest prefix wins; JSON form
	m2, err := ParsePathMap(`{"/data/": "/a/", "/data/videos/": "/b/"}`)
	if err != nil || m2.Len() != 2 {
		t.Fatal(err)
	}
	if got := m2.ToLocal("/data/videos/x"); got != "/b/x" {
		t.Errorf("longest prefix: %q", got)
	}
	if _, err := ParsePathMap(`{bad`); err == nil {
		t.Error("bad JSON accepted")
	}
	if m3, _ := ParsePathMap(""); m3.Len() != 0 {
		t.Error("empty map")
	}
}

func TestShareRootOf(t *testing.T) {
	lr := func(id, p string, n int64) LibraryRoot { return LibraryRoot{LibraryID: id, Path: p, Videos: n} }
	cases := []struct {
		name  string
		roots []LibraryRoot
		want  string
	}{
		{"none", nil, ""},
		{"one", []LibraryRoot{lr("a", "/Volumes/nas/videos/Channel", 3)}, "/Volumes/nas/videos/Channel"},
		{"common", []LibraryRoot{lr("a", "/Volumes/nas/videos/A", 1), lr("b", "/Volumes/nas/videos/B/", 9)}, "/Volumes/nas/videos"},
		{"windows case", []LibraryRoot{lr("a", `Y:\Media\A`, 1), lr("b", "y:/media/B", 1)}, "Y:/Media"},
		{"unc", []LibraryRoot{lr("a", "//nas/share/v/A", 1), lr("b", "//nas/share/v/B", 1)}, "//nas/share/v"},
		{"root only: most videos", []LibraryRoot{lr("a", "/Volumes/one/A", 5), lr("b", "/srv/two/B", 50)}, "/srv/two/B"},
		{"drives differ: most videos", []LibraryRoot{lr("a", "Y:/A", 70), lr("b", "O:/B", 50)}, "Y:/A"},
		{"same drive root only", []LibraryRoot{lr("a", "Y:/A", 1), lr("b", "Y:/B", 2)}, "Y:/B"},
		{"winner with two roots", []LibraryRoot{lr("a", "/x/A", 1), lr("b", "/m/B/one", 9), lr("b", "/m/B/two", 9)}, "/m/B"},
	}
	for _, c := range cases {
		if got := ShareRootOf(c.roots); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLocatorResolve(t *testing.T) {
	base := t.TempDir()
	share := filepath.Join(base, "Volumes", "Y")
	_ = os.MkdirAll(filepath.Join(share, "Channel", "A"), 0o755)
	fallback := filepath.Join(base, "local")
	pm, _ := ParsePathMap("Y:/=" + filepath.ToSlash(share))
	roots := func(context.Context) ([]LibraryRoot, error) {
		return []LibraryRoot{{LibraryID: "a", Path: "Y:/Channel/A", Videos: 1}, {LibraryID: "b", Path: "Y:/Channel/B", Videos: 1}}, nil
	}
	ctx := context.Background()

	l := &Locator{PathMap: pm, Roots: roots, Fallback: fallback}
	loc := l.Resolve(ctx)
	want := filepath.Join(share, "Channel", "maktaba", "cluster")
	if !loc.OnShare || loc.Dir != want || loc.Reason != nil || loc.ShareRoot != "Y:/Channel" {
		t.Fatalf("on share: %+v", loc)
	}
	if !exists(want) {
		t.Fatal("cluster dir not created")
	}

	// no map for the share: the local fallback, with the reason
	l2 := &Locator{Roots: roots, Fallback: fallback}
	loc = l2.Resolve(ctx)
	if loc.OnShare || loc.Dir != fallback || loc.Reason == nil || !strings.Contains(*loc.Reason, "MAKTABA_PATH_MAP") {
		t.Fatalf("unmapped: %+v", loc)
	}

	// an unmounted share (an empty mount point) is not written to
	empty := filepath.Join(base, "Volumes", "Z")
	_ = os.MkdirAll(empty, 0o755)
	l3 := &Locator{ShareRoot: filepath.ToSlash(empty), Fallback: fallback}
	loc = l3.Resolve(ctx)
	if loc.OnShare || !strings.Contains(*loc.Reason, "not reachable") || exists(filepath.Join(empty, "maktaba")) {
		t.Fatalf("unmounted: %+v", loc)
	}

	// MAKTABA_CLUSTER_DIR wins
	over := filepath.Join(base, "override")
	loc = (&Locator{Override: over, PathMap: pm, Roots: roots}).Resolve(ctx)
	if loc.Dir != over || loc.OnShare {
		t.Fatalf("override: %+v", loc)
	}

	// the roots cannot be read
	loc = (&Locator{Roots: func(context.Context) ([]LibraryRoot, error) { return nil, errors.New("db down") }, Fallback: fallback}).Resolve(ctx)
	if loc.OnShare || !strings.Contains(*loc.Reason, "db down") {
		t.Fatalf("roots error: %+v", loc)
	}
}

func TestHomeKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "home-agent.key")
	k1, err := LoadOrCreateHomeKey(p)
	if err != nil || len(k1) != 32 {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	k2, _ := LoadOrCreateHomeKey(p)
	if string(k1) != string(k2) {
		t.Fatal("key not reused")
	}
	_ = os.Chmod(p, 0o644)
	_, _ = LoadOrCreateHomeKey(p)
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatal("mode not tightened")
	}
	_ = os.WriteFile(p, []byte("short"), 0o600)
	k3, _ := LoadOrCreateHomeKey(p)
	if len(k3) != 32 || string(k3) == string(k1) {
		t.Fatal("a broken key was not replaced")
	}
	if HomeKeyPath("/x/y") != "/x/y" || !strings.HasSuffix(HomeKeyPath(""), filepath.Join(".maktaba", "cluster", "home-agent.key")) {
		t.Fatal("HomeKeyPath")
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{
		"MAKTABA_CLUSTER_RETENTION_DAYS": "7",
		"MAKTABA_CLUSTER_FILE_MB":        "10",
		"MAKTABA_CLUSTER_LOG_LEVEL":      "WARN",
		"MAKTABA_LLM_QUEUE_TIMEOUT_S":    "30",
		"MAKTABA_LLM_MAX_PARALLEL":       "nope",
		"MAKTABA_PATH_MAP":               "Y:/=/Volumes/Y",
		"MAKTABA_CLUSTER_DIR":            "/tmp/c",
		"MAKTABA_CORS_ALLOWED_ORIGINS":   "http://localhost:5173, https://maktaba.example/",
	}
	cfg, warn := FromEnv(func(k string) string { return env[k] })
	if cfg.RetentionDays != 7 || cfg.FileMaxBytes != 10<<20 || cfg.LogLevel != "warning" ||
		cfg.LLMQueueTimeout != 30*time.Second || cfg.LLMMaxParallel != DefaultMaxParallel ||
		cfg.Locator.Override != "/tmp/c" || cfg.Locator.PathMap.Len() != 1 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "MAKTABA_LLM_MAX_PARALLEL") {
		t.Fatalf("warnings = %v", warn)
	}
	if len(cfg.BrowserOrigins) != 2 || cfg.BrowserOrigins[0] != "localhost:5173" || cfg.BrowserOrigins[1] != "maktaba.example" {
		t.Fatalf("origins = %v", cfg.BrowserOrigins)
	}
	cfg, _ = FromEnv(func(string) string { return "" })
	if cfg.LogLevel != "info" || cfg.RetentionDays != 14 || cfg.FileMaxBytes != 256<<20 || cfg.AgentRatePerMin != 60 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestIPDirAndValidation(t *testing.T) {
	if IPDir("fe80::1%en0") != "fe80--1-en0" || IPDir("10.0.0.2") != "10.0.0.2" {
		t.Fatal("IPDir")
	}
	for s, ok := range map[string]bool{
		"10.0.0.2": true, "fe80--1": true, "studio": true,
		"..": false, ".": false, "a/b": false, "a..b": false, "": false, `a\b`: false, "-x": false,
	} {
		if ValidIPDir(s) != ok {
			t.Errorf("ValidIPDir(%q) != %v", s, ok)
		}
	}
	for s, ok := range map[string]bool{"2026-10-19": true, "2026-02-30": false, "2026-1-1": false, "../2026": false} {
		if ValidDate(s) != ok {
			t.Errorf("ValidDate(%q) != %v", s, ok)
		}
	}
}
