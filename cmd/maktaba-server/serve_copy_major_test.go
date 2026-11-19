package main

import (
	"os"
	"path/filepath"
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
	if aside, err := setAsideOtherMajor(data, "18", time.Now()); err != nil || aside != "" {
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
	if aside, err := setAsideOtherMajor(data, "16", time.Now()); err != nil || aside != "" {
		t.Fatalf("a copy of its own major: %q %v", aside, err)
	}
	now := time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC)
	aside, err := setAsideOtherMajor(data, "18", now)
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
