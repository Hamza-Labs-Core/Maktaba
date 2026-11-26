package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Epic 49 story 49.3: a machine's copy runs on a Postgres of either major its payloads
// carry; a copy another major made is set aside (never deleted) to be made again, one of
// its own major is kept.
func TestCopyOfTheOtherMajorIsSetAside(t *testing.T) {
	for m, want := range map[string]bool{"16": true, "18": true, "17": false, "15": false, "": false} {
		if copyMajor(m) != want {
			t.Errorf("copyMajor(%q) = %v", m, !want)
		}
	}
	data := filepath.Join(t.TempDir(), "postgres")
	if aside, err := setAsideCluster(data, "18", false, time.Now()); err != nil || aside != "" {
		t.Fatalf("no copy: %q %v", aside, err)
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "maktaba-copy.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if aside, err := setAsideCluster(data, "16", false, time.Now()); err != nil || aside != "" {
		t.Fatalf("a copy of its own major: %q %v", aside, err)
	}
	now := time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC)
	aside, err := setAsideCluster(data, "18", false, now)
	if err != nil || aside != data+".pg16-20261119-160000" {
		t.Fatalf("a 16 copy under 18's programs: %q %v", aside, err)
	}
	if _, err := os.Stat(filepath.Join(aside, "maktaba-copy.json")); err != nil {
		t.Errorf("the copy's files did not move with it: %v", err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Errorf("the copy's directory is still there: %v", err)
	}
}

// The other major's directory: any cluster there is set aside (a promotion would find it),
// whatever its major.
func TestTheOtherDirectorysClusterIsSetAside(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "postgres")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("18\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	aside, err := setAsideCluster(dir, "18", true, time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC))
	if err != nil || aside != dir+".pg18-20261119-200000" {
		t.Fatalf("set aside: %q %v", aside, err)
	}
}

func TestCopyAsidePreservesAndIdentifiesPreJoinDatabase(t *testing.T) {
	for _, oldCopy := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "postgres")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"PG_VERSION": "16", "data": "the original database"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want := "this machine's own database"
		if oldCopy {
			if err := os.WriteFile(filepath.Join(dir, "maktaba-copy.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			want = "the old copy"
		}
		aside, err := setAsideCluster(dir, "18", true, time.Now())
		if err != nil || aside == "" {
			t.Fatalf("set aside: %q, %v", aside, err)
		}
		var log bytes.Buffer
		logCopyClusterAside(&log, "18", dir, aside)
		if !strings.Contains(log.String(), want) || !strings.Contains(log.String(), aside) {
			t.Fatal(log.String())
		}
		if err := os.Rename(aside, dir); err != nil {
			t.Fatalf("put back for rollback: %v", err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "data")); err != nil || string(got) != "the original database" {
			t.Fatalf("rollback data = %q, %v", got, err)
		}
	}
}
