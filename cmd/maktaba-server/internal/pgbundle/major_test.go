package pgbundle

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Epic 49 story 49.2: 16 and older keep <root>/postgres (every cluster made before), 18 and
// later have their own directory.
func TestDataDirFor(t *testing.T) {
	for major, want := range map[string]string{"16": "postgres", "": "postgres", "x": "postgres", "17": "postgres",
		"18": "postgres-18", "19": "postgres-19"} {
		if got := filepath.Base(DataDirFor("/r", major)); got != want {
			t.Errorf("DataDirFor(%q) = %s, want %s", major, got, want)
		}
	}
}

// A payload runs postgres-18/ when it has one, and keeps postgres/ as the previous major's
// programs for the upgrade; one with postgres/ alone runs it and has no previous major.
func TestPayloadHomePrefersTheNewerMajor(t *testing.T) {
	p := t.TempDir()
	server := func(dir string) {
		if err := os.MkdirAll(filepath.Join(p, dir, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, dir, "bin", exe("postgres")), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := payloadHome(p); got != "" {
		t.Fatalf("no Postgres: %q", got)
	}
	server("postgres")
	if got := payloadHome(p); got != filepath.Join(p, "postgres") || PreviousHome(got) != "" {
		t.Fatalf("postgres/ alone: %q, previous %q", got, PreviousHome(got))
	}
	server("postgres-18")
	got := payloadHome(p)
	if got != filepath.Join(p, "postgres-18") || PreviousHome(got) != filepath.Join(p, "postgres") {
		t.Fatalf("with postgres-18/: %q, previous %q", got, PreviousHome(got))
	}
}

// The hold: recorded with its reason, read back, taken back (twice is fine).
func TestMajorHold(t *testing.T) {
	root := t.TempDir()
	if HeldMajor(root) != "" {
		t.Fatal("held before any hold")
	}
	if err := HoldMajor(root, "no room for the upgraded copy"); err != nil {
		t.Fatal(err)
	}
	if got := HeldMajor(root); got != "no room for the upgraded copy" {
		t.Fatalf("HeldMajor = %q", got)
	}
	if err := ReleaseMajor(root); err != nil || HeldMajor(root) != "" {
		t.Fatalf("ReleaseMajor: %v, still %q", err, HeldMajor(root))
	}
	if err := ReleaseMajor(root); err != nil {
		t.Fatal(err)
	}
}

// Needed: nothing to upgrade without an old cluster; a new cluster with no marker was made
// fresh and stays; a marker with no checkpoint is an upgrade that did not finish.
func TestMajorUpgradeNeeded(t *testing.T) {
	root := t.TempDir()
	u := MajorUpgrade{Old: Bundle{Data: filepath.Join(root, "postgres")}, New: Bundle{Data: filepath.Join(root, "postgres-18")}}
	ctx := context.Background()
	if need, _, err := u.Needed(ctx); err != nil || need {
		t.Fatalf("no old cluster: %v %v", need, err)
	}
	for _, d := range []string{u.Old.Data, u.New.Data} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "PG_VERSION"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if need, why, err := u.Needed(ctx); err != nil || need || why != "made fresh" {
		t.Fatalf("a fresh new cluster: %v %q %v", need, why, err)
	}
	if err := os.WriteFile(filepath.Join(u.New.Data, UpgradeMarker), []byte(`{"major":"16"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if need, why, err := u.Needed(ctx); err != nil || !need || why != "an upgrade that did not finish" {
		t.Fatalf("an unfinished upgrade: %v %q %v", need, why, err)
	}
}

// PostgreSQL 18 runs with io_method=sync (no I/O worker processes: the idle memory
// budget); 16, which has no such setting, never gets it.
func TestServerArgsIOMethodOn18Only(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script as postgres")
	}
	for major, want := range map[string]bool{"16": false, "18": true} {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\necho 'postgres (PostgreSQL) " + major + ".7'\n"
		if err := os.WriteFile(filepath.Join(home, "bin", "postgres"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		args := strings.Join(Bundle{Home: home, Data: t.TempDir(), Port: 5499}.serverArgs(false), " ")
		if got := strings.Contains(args, "io_method=sync"); got != want {
			t.Errorf("PostgreSQL %s: io_method=sync %v, want %v (%s)", major, got, want, args)
		}
	}
}
