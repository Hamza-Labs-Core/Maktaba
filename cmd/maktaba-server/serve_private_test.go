package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// userOnly fails t unless the file at path is this user's alone (on Windows a protected
// DACL that grants this user and no one else, mode 0600 elsewhere).
func userOnly(t *testing.T, path, what string) {
	t.Helper()
	ok, err := config.IsUserOnly(path)
	if err != nil || !ok {
		t.Fatalf("%s (%s) is not the user's alone: %v", what, path, err)
	}
}

// A switch's records and the identity a machine keeps for it are the user's alone,
// whatever their folder lets others do (a state folder on a second drive: Authenticated
// Users may read and change what takes its ACL); the record they replace goes, ACL and
// all, and no temp file stays. An identity an older version kept with its folder's ACL
// is made the user's alone when it is read.
func TestPrivateFilesAreTheUsersAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "switchover", "sw-1")
	progress := filepath.Join(dir, "progress.json")
	for _, body := range []string{`{"state":"asked"}`, `{"state":"done"}`} {
		if err := writePrivate(progress, []byte(body)); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(progress); err != nil || string(b) != body {
			t.Fatalf("progress.json = %q, %v; want %q", b, err, body)
		}
		userOnly(t, progress, "a switch's record")
	}
	// one an older version wrote: a new file by plain os.WriteFile, the folder's ACL (or
	// mode 0644)
	if err := os.Remove(progress); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(progress, []byte(`{}`), 0o644); err != nil { //nolint:gosec // the test's own
		t.Fatal(err)
	}
	if ok, _ := config.IsUserOnly(progress); ok {
		t.Log("this temp folder gives a new file to its user alone (umask 077?): no repair seen here")
	}
	if err := writePrivate(progress, []byte(`{"state":"done"}`)); err != nil {
		t.Fatal(err)
	}
	userOnly(t, progress, "a record written over one with its folder's ACL")

	made, err := identityAt(filepath.Join(dir, "old-home.json"))
	if err != nil {
		t.Fatal(err)
	}
	userOnly(t, filepath.Join(dir, "old-home.json"), "a new identity")

	for i, read := range []func(string) (oldHome, error){identityAt, readIdentity} {
		kept := filepath.Join(dir, "kept-"+string(rune('a'+i))+".json")
		b, _ := os.ReadFile(filepath.Join(dir, "old-home.json"))
		if err := os.WriteFile(kept, b, 0o644); err != nil { //nolint:gosec // the test's own
			t.Fatal(err)
		}
		me, err := read(kept)
		if err != nil || me.NodeID != made.NodeID || me.PrivateKey != made.PrivateKey {
			t.Fatalf("the kept identity read as %+v, %v; want %s", me, err, made.NodeID)
		}
		userOnly(t, kept, "an identity an older version kept")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a temp file stayed: %s", e.Name())
		}
	}
}

// A switch's folder is named by its id's last element, and an id that names no folder of
// its own there gets "_": a request never reaches past the switchover folder.
func TestASwitchFolderStaysInTheSwitchoverFolder(t *testing.T) {
	s := &switching{}
	s.cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	root := filepath.Join(s.cfg.StateDir(), "switchover")
	for id, want := range map[string]string{
		"sw-1":                                 "sw-1",
		"7f9c2b1e-4d3a-4b8e-9a6f-0c1d2e3f4a5b": "7f9c2b1e-4d3a-4b8e-9a6f-0c1d2e3f4a5b",
		"standby:n1":                           "_",
		".hidden":                              "_",
		"../../keys":                           "keys",
		"..":                                   "_",
		filepath.Join("..", ".."):              "_",
		filepath.Join("a", ".."):               "_",
		"":                                     "_",
		".":                                    "_",
		"sw-2" + string(os.PathSeparator):      "sw-2",
	} {
		if got := s.dir(id); got != filepath.Join(root, want) {
			t.Errorf("dir(%q) = %s, want %s", id, got, filepath.Join(root, want))
		}
	}
}

// The temp files a write of a record left when its process stopped mid-write (they may
// hold a secret) go with the next write of it; one made in the last minute may be
// another write's, and stays, as does another record's.
func TestPrivateWritesSweepTheirOldTempFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "switchover", "sw-1")
	path := filepath.Join(dir, "old-home.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	files := map[string]bool{ // name: kept after the write
		"old-home.json.4242-0badf00d.tmp":  false,
		"old-home.json.tmp":                false, // the versions before
		"old-home.json.4243-0badf00e.tmp":  true,  // a write of the last minute
		"progress.json.4242-0badf00d.tmp":  true,  // another record's
		"old-home.json.4242-0badf00d.tmpx": true,
	}
	for name := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if name != "old-home.json.4243-0badf00e.tmp" {
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writePrivate(path, []byte(`{"node_id":"n1"}`)); err != nil {
		t.Fatal(err)
	}
	for name, kept := range files {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != kept {
			t.Errorf("%s: there %v, want %v", name, err == nil, kept)
		}
	}
}
