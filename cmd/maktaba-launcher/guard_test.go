package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// mkPayload makes dir an app payload of version (payload.json, the test binary as its
// maktaba-server) acting out behavior and tracing as label.
func mkPayload(t *testing.T, dir, version, behavior, label string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := layout.CopyFile(selfBinary(t), filepath.Join(bin, layout.Exe("maktaba-server"))); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{filepath.Join(bin, "behavior"): behavior, filepath.Join(bin, "label"): label,
		filepath.Join(dir, payloadJSON): `{"schema": 1, "platform": "` + payloadPlatform() + `", "version": "` + version + `"}`} {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeRecord writes a database record saying version's migrations ran over from's
// database, dumped first.
func writeRecord(t *testing.T, path, version, from string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"version": version, "state": "migrated", "from": from, "dump": "/backups/pre.dump"})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func recordState(t *testing.T, path string) (state, failed string) {
	t.Helper()
	var r struct {
		State  string `json:"state"`
		Failed string `json:"failed"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &r)
	return r.State, r.Failed
}

// An app update whose new server cannot stay up (Epic 35, install-kinds.md): the guard
// runs the kept copy of the payload that ran before, and marks the database record for
// it to put back the dump taken before the new release's migrations.
func TestBundleGuardRunsTheKeptPayload(t *testing.T) {
	t.Parallel()
	processTest(t)
	bundle, root := mkBundle(t, "crash")
	mkPayload(t, bundle, "2.0.0", "crash", "new")
	kept := filepath.Join(root, "updates", "payloads", "1.0.0")
	mkPayload(t, kept, "1.0.0", "run", "old")
	record := filepath.Join(root, "database.json")
	writeRecord(t, record, "2.0.0", "1.0.0")
	if err := layout.WriteGuard(root, layout.Guard{Version: "2.0.0", RunID: "run-1", Since: time.Now(),
		PreviousPayload: kept, Record: record}); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.Bundle = bundle
	startLauncher(t, cfg)
	l := layout.New(root)
	waitFor(t, "the kept payload runs", func() bool { return countAct(readTrace(l), "old", "run") == 1 })
	if n := countAct(readTrace(l), "new", "crash"); n != 3 {
		t.Fatalf("the new payload started %d times, want 3", n)
	}
	if o := layout.ReadOverride(root); o != kept {
		t.Fatalf("override %q, want %q", o, kept)
	}
	if g, _ := layout.ReadGuard(root); g != nil {
		t.Fatalf("guard left: %+v", g)
	}
	if st, failed := recordState(t, record); st != "restoring" || failed != "2.0.0" {
		t.Fatalf("record %s failed %s", st, failed)
	}
	rb := events(l, "launcher_rollback")
	if len(rb) != 1 || rb[0].Detail["reason"] != "boot_loop" || rb[0].Detail["to"] != kept {
		t.Fatalf("launcher_rollback = %+v", rb)
	}
}

// The override runs the kept payload with the app's own named beside it, and the
// launcher's root for the server's guard; a guard naming another release is not this
// payload's (no fallback).
func TestBundleOverrideEnvironment(t *testing.T) {
	t.Parallel()
	bundle, root := mkBundle(t, "run")
	mkPayload(t, bundle, "2.0.0", "run", "new")
	kept := filepath.Join(root, "updates", "payloads", "1.0.0")
	mkPayload(t, kept, "1.0.0", "run", "old")
	ln := &launcher{cfg: Config{Root: root, Bundle: bundle}, l: layout.New(root), log: newEventLog(layout.New(root).LauncherLog(), nil, time.Now)}
	defer ln.log.Close()
	if p := ln.bundlePayload(); p != bundle {
		t.Fatalf("without an override: %q", p)
	}
	if err := layout.WriteOverride(root, kept); err != nil {
		t.Fatal(err)
	}
	if p := ln.bundlePayload(); p != kept {
		t.Fatalf("with an override: %q", p)
	}
	_ = layout.WriteGuard(root, layout.Guard{Version: "2.0.0", Since: time.Now()})
	if ln.isPending(bundleRelease) {
		t.Fatal("a guard of 2.0.0 is not the kept 1.0.0's")
	}
	_ = layout.WriteOverride(root, "")
	if !ln.isPending(bundleRelease) {
		t.Fatal("the bundle's 2.0.0 is pending")
	}
}

// A Linux runtime's guard moves `current` back to the directory that ran before.
func TestRuntimeGuardMovesCurrentBack(t *testing.T) {
	t.Parallel()
	processTest(t)
	if runtime.GOOS == "windows" {
		t.Skip("the runtime copy is the Linux app's (a link renamed over a directory link)")
	}
	rt := t.TempDir()
	mkPayload(t, filepath.Join(rt, "v1"), "1.0.0", "run", "v1")
	mkPayload(t, filepath.Join(rt, "v2"), "2.0.0", "crash", "v2")
	if err := os.Symlink("v2", filepath.Join(rt, runtimeCurrent)); err != nil {
		t.Skipf("symlinks: %v", err)
	}
	root := t.TempDir()
	if err := layout.WriteGuard(root, layout.Guard{Version: "2.0.0", Since: time.Now(), PreviousRuntime: "v1"}); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.Bundle, cfg.RuntimeRoot = filepath.Join(rt, runtimeCurrent), rt
	startLauncher(t, cfg)
	l := layout.New(root)
	waitFor(t, "v1 runs again", func() bool { return countAct(readTrace(l), "v1", "run") == 1 })
	if target, _ := os.Readlink(filepath.Join(rt, runtimeCurrent)); target != "v1" {
		t.Fatalf("current -> %q", target)
	}
}

// The layout's guard marks the database record too (the pending marker names it).
func TestLayoutRollbackMarksTheRecord(t *testing.T) {
	t.Parallel()
	processTest(t)
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "crash"}, "1.0.0")
	if err := l.Switch("2.0.0", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(l.Root, "database.json")
	writeRecord(t, record, "2.0.0", "1.0.0")
	if err := l.GuardPending("2.0.0", time.Now().Add(time.Hour), record); err != nil {
		t.Fatal(err)
	}
	startLauncher(t, testConfig(l.Root))
	waitFor(t, "previous started", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	if st, failed := recordState(t, record); st != "restoring" || failed != "2.0.0" {
		t.Fatalf("record %s failed %s", st, failed)
	}
}

// The orchestrator's deadline holds the pending release past the launcher's own
// timeout (a long migration at its start).
func TestPendingDeadlineExtendsTheTimeout(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
	if err := l.Switch("2.0.0", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ln := &launcher{cfg: Config{Root: l.Root, PendingTimeout: time.Minute}, l: l, firstStart: now}
	if d := ln.deadline("2.0.0"); !d.Equal(now.Add(time.Minute)) {
		t.Fatalf("no deadline set: %v", d)
	}
	if err := l.GuardPending("2.0.0", now.Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	if d := ln.deadline("2.0.0"); d.Sub(now) < 59*time.Minute {
		t.Fatalf("deadline %v", d)
	}
}
