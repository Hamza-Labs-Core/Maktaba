package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The share is writable by every machine: links planted under the
// cluster folder must not let the hub write, delete or read outside it.

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, _ os.DirEntry, _ error) error {
		out = append(out, p)
		return nil
	})
	return out
}

func TestWriterRefusesLinks(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	cdir := filepath.Join(base, "cluster")
	_ = os.MkdirAll(outside, 0o755)
	_ = os.MkdirAll(cdir, 0o755)
	victim := filepath.Join(outside, "victim.txt")
	_ = os.WriteFile(victim, []byte("keep"), 0o600)
	date := time.Now().UTC().Format("2006-01-02")

	symlinkOrSkip(t, outside, filepath.Join(cdir, "10.0.0.1"))                                  // the machine folder
	symlinkOrSkip(t, outside, filepath.Join(cdir, "10.0.0.2", KindEvents, date))                // the day folder
	symlinkOrSkip(t, victim, filepath.Join(cdir, "10.0.0.3", KindEvents, date, hubFile))        // the file itself
	symlinkOrSkip(t, "../../../outside", filepath.Join(cdir, "10.0.0.4", KindEvents, date+"x")) // relative, unused
	before := dirEntries(t, outside)

	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		fw := NewFileWriter(4, 0, time.Now, quiet())
		fw.SetTarget(cdir, ip)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); fw.Run(ctx) }()
		fw.Write(KindEvents, map[string]any{"event": "x"})
		waitFor(t, ip+" refused", func() bool { return fw.Stats().Errors > 0 })
		cancel()
		<-done
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("victim written: %q", b)
	}
	if after := dirEntries(t, outside); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("outside changed: %v", after)
	}
}

func TestSweeperRefusesLinks(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	cdir := filepath.Join(base, "cluster")
	// outside holds day-named folders a link could expose to the sweep
	_ = os.MkdirAll(filepath.Join(outside, "2020-01-01"), 0o755)
	_ = os.WriteFile(filepath.Join(outside, "2020-01-01", "precious"), []byte("x"), 0o600)
	old := time.Now().AddDate(0, 0, -100)
	_ = os.Chtimes(filepath.Join(outside, "2020-01-01", "precious"), old, old)
	_ = os.MkdirAll(cdir, 0o755)

	symlinkOrSkip(t, outside, filepath.Join(cdir, "10.0.0.7"))                                                    // machine folder link
	symlinkOrSkip(t, outside, filepath.Join(cdir, "10.0.0.8", "logs"))                                            // kind folder link
	symlinkOrSkip(t, filepath.Join(outside, "2020-01-01"), filepath.Join(cdir, "10.0.0.9", "logs", "2020-01-01")) // day link
	symlinkOrSkip(t, outside, filepath.Join(cdir, "10.0.0.1", KindEvents))                                        // the home's own kind
	for _, d := range []string{"10.0.0.8", "10.0.0.9", "10.0.0.1"} {
		_ = os.Chtimes(filepath.Join(cdir, d), old, old)
	}

	s := &Sweeper{Days: 14, Now: time.Now}
	if _, err := s.Sweep(cdir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "2020-01-01", "precious")); err != nil || string(b) != "x" {
		t.Fatalf("outside deleted: %v", err)
	}
}

func TestReadersRefuseLinks(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(filepath.Dir(f.dir), "outside-"+filepath.Base(f.dir))
	writeJSONL(t, filepath.Join(outside, "secret.jsonl"), `{"ts":"2026-10-19T10:00:00Z","event":"secret"}`)
	writeJSONL(t, filepath.Join(outside, "p.json"), `{"secret":true}`)
	symlinkOrSkip(t, filepath.Join(outside, "secret.jsonl"), filepath.Join(f.dir, "10.0.0.5", "logs", day, "w1.jsonl"))
	symlinkOrSkip(t, filepath.Join(outside, "p.json"), filepath.Join(f.dir, "10.0.0.5", "processes", "w1.json"))
	symlinkOrSkip(t, outside, filepath.Join(f.dir, "10.0.0.6", "logs", day))

	for _, ip := range []string{"10.0.0.5", "10.0.0.6"} {
		if code, rs := f.records("/api/cluster/files/" + ip + "/logs?date=" + day); code != 200 || len(rs.Records) != 0 {
			t.Fatalf("%s: link followed: %d %+v", ip, code, rs)
		}
	}
	var idx FilesIndex
	decode(t, f.do("GET", "/api/cluster/files", "", "admin"), &idx)
	for _, m := range idx.Nodes {
		if m.IP == "10.0.0.5" && len(m.Processes) != 0 {
			t.Fatalf("linked process file read: %s", m.Processes)
		}
		if m.IP == "10.0.0.6" && len(m.Dates["logs"]) != 0 {
			t.Fatalf("linked day listed: %v", m.Dates)
		}
	}
}

func TestLocatorRefusesLinkedClusterDir(t *testing.T) {
	base := t.TempDir()
	share := filepath.Join(base, "share")
	elsewhere := filepath.Join(base, "elsewhere")
	_ = os.MkdirAll(filepath.Join(share, "videos"), 0o755)
	_ = os.MkdirAll(elsewhere, 0o755)
	symlinkOrSkip(t, elsewhere, filepath.Join(share, "maktaba"))
	loc := (&Locator{ShareRoot: filepath.ToSlash(share), Fallback: filepath.Join(base, "local")}).Resolve(context.Background())
	if loc.OnShare || loc.Reason == nil || !strings.Contains(*loc.Reason, "cannot use") {
		t.Fatalf("linked cluster dir used: %+v", loc)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "cluster")); err == nil {
		t.Fatal("created through the link")
	}
}
