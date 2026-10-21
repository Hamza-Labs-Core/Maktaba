package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// The terminal setup takes the app's steps: a refused answer is asked again
// with the step's reason, and what it saves is what the app's finish saves.
func TestSetupCLIDrivesTheSteps(t *testing.T) {
	spawnsPrograms(t) // the api's hash-password and adduser stand-ins
	e := newSetupEnv(t)
	prevDB, prevGPUs := setupDatabase, hostGPUs
	t.Cleanup(func() { setupDatabase, hostGPUs = prevDB, prevGPUs })
	hostGPUs = func() []hostinfo.GPU { return nil }
	var dbURL string
	setupDatabase = func(s *setupRun, fn func(string) error) error {
		dbURL = s.cfg.Database.URL
		return fn(dbURL)
	}
	lib := filepath.Join(e.dir, "Lectures")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	answers := strings.Join([]string{
		"",                        // data directory: the config's
		"",                        // backend: sqlite
		"admin", "short", "short", // refused: under 8 characters
		"admin", "a long one", "x", // the two passwords differ
		"admin", "a long one", "a long one",
		"Lectures", "", // refused: not a whole path
		lib, "/Volumes/Media => " + e.dir,
		"n", // Plex: later
		"n", // no model download now
	}, "\n") + "\n"
	if err := runSetupWith(newPrompterFrom(strings.NewReader(answers))); err != nil {
		t.Fatal(err)
	}
	if want := "sqlite://" + filepath.Join(e.state, "maktaba.db"); dbURL != want {
		t.Fatalf("database %q, want %q", dbURL, want)
	}
	saved, found, err := config.LoadFrom(e.config)
	wantMap, _ := json.Marshal(map[string]string{"/Volumes/Media": e.dir})
	if err != nil || !found || len(saved.Media.Roots) != 1 || saved.Media.Roots[0] != lib ||
		saved.Media.PathMap != string(wantMap) || !saved.DatabaseURLSet {
		t.Fatalf("server.toml: %+v %v", saved.Media, err)
	}
	var seeded bool
	for _, c := range fakeCalls(t, e.fakeLog) {
		seeded = seeded || (c.Args[0] == "adduser" && c.Args[len(c.Args)-1] == "admin")
	}
	if !seeded {
		t.Fatal("the admin was not seeded through the finish step")
	}
	if _, err := os.Stat(filepath.Join(e.state, "state", "setup.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the pending answers stay after finish: %v", err)
	}
}

// finish creates the SQLite file's folder of an install outside the app.
func TestSetupDatabaseSQLite(t *testing.T) {
	e := newSetupEnv(t)
	cfg := config.Defaults()
	cfg.Server.DataDir = e.state
	cfg.Database.URL = "sqlite://" + filepath.Join(e.dir, "db", "maktaba.db")
	s := &setupRun{ctx: context.Background(), cfg: cfg, out: json.NewEncoder(io.Discard), log: io.Discard}
	var got string
	if err := setupDatabase(s, func(u string) error { got = u; return nil }); err != nil {
		t.Fatal(err)
	}
	if got != cfg.Database.URL {
		t.Fatalf("fn got %q", got)
	}
	if info, err := os.Stat(filepath.Join(e.dir, "db")); err != nil || !info.IsDir() {
		t.Fatalf("the database's folder: %v", err)
	}
	s.cfg.Database.URL = "mysql://x"
	var se *stepError
	if err := setupDatabase(s, func(string) error { return nil }); !errors.As(err, &se) || se.code != "database_unsupported" || se.params["scheme"] != "mysql" {
		t.Fatalf("an unknown scheme: %v", err)
	}
}

// A cancelled venv step asks the bootstrap to stop, which says where it was
// (interrupted, exit 130), instead of killing it with pip under it.
func TestSetupVenvStopsGently(t *testing.T) {
	spawnsPrograms(t)
	if runtime.GOOS == "windows" {
		t.Skip("CTRL_BREAK needs a console the test shares with the stand-in; stopGently's Windows part is TestStopGentlyWindows")
	}
	e := newSetupEnv(t)
	fakePayload(t, e)
	t.Setenv("MAKTABA_TEST_FAKE_FAIL", "venv-wait")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	s := &setupRun{ctx: ctx, cfg: cfg, out: json.NewEncoder(pw), log: io.Discard}
	done := make(chan error, 1)
	go func() {
		_, err := stepVenv(s, nil)
		_ = pw.Close()
		done <- err
	}()
	// the first progress line: the build runs; then the stop
	dec := json.NewDecoder(pr)
	var ev setupEvent
	if err := dec.Decode(&ev); err != nil || ev.Stage != "venv" {
		t.Fatalf("first event %+v %v", ev, err)
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	start := time.Now()
	cancel()
	err = <-done
	var se *stepError
	if !errors.As(err, &se) || se.code != "interrupted" {
		t.Fatalf("a stopped build: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("stopped after %s: asked, not waited out", time.Since(start))
	}
}
