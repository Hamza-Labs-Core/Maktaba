package main

import (
	"os"
	"path/filepath"
	"testing"
)

// An app payload carries its migrations beside bin/: maktaba-api finds them
// wherever it runs from (the desktop apps and the Linux runtime run it in the
// user's data directory, never in a checkout).
func TestMigrationsDirFor(t *testing.T) {
	payload := t.TempDir()
	exe := filepath.Join(payload, "bin", "maktaba-api")
	if err := os.MkdirAll(filepath.Join(payload, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if got := migrationsDirFor("", exe, elsewhere); got != filepath.Join(payload, "migrations") {
		t.Errorf("payload: %s", got)
	}
	if got := migrationsDirFor("/env/dir", exe, elsewhere); got != "/env/dir" {
		t.Errorf("MAKTABA_MIGRATIONS_DIR first: %s", got)
	}

	// a binary outside a payload: shared/db/migrations above the working directory
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "shared", "db", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "api", "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := migrationsDirFor("", filepath.Join(t.TempDir(), "maktaba-api"), sub); got != filepath.Join(repo, "shared", "db", "migrations") {
		t.Errorf("checkout: %s", got)
	}
	if got := migrationsDirFor("", "", ""); got != "shared/db/migrations" {
		t.Errorf("nothing known: %s", got)
	}
}

// A plain server release keeps its migrations beside its binaries (Epic 35:
// bin/releases/<v>/migrations), and its maktaba-api finds them there.
func TestMigrationsDirForARelease(t *testing.T) {
	rel := filepath.Join(t.TempDir(), "bin", "releases", "1.5.0")
	if err := os.MkdirAll(filepath.Join(rel, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := migrationsDirFor("", filepath.Join(rel, "maktaba-api"), t.TempDir()); got != filepath.Join(rel, "migrations") {
		t.Errorf("release: %s", got)
	}
}
