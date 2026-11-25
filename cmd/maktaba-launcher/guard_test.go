package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
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

// stubPayload makes dir an app payload of version whose maktaba-server is a few bytes,
// not the test binary: for a test that only chooses a payload and never starts one.
// Copying the race-built test binary (tens of MB, synced) per payload made such a test
// slow on a CI runner (#1428).
func stubPayload(t *testing.T, dir, version string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{filepath.Join(bin, layout.Exe("maktaba-server")): "stub",
		filepath.Join(dir, payloadJSON): `{"schema": 1, "platform": "` + payloadPlatform() + `", "version": "` + version + `"}`} {
		if err := os.WriteFile(name, []byte(body), 0o755); err != nil {
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
// runs the kept copy of the payload that ran before, and marks the database record held
// (never restoring): the kept copy stops on it and a person decides, nothing is put back.
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
	// held, never restoring: the kept payload stops on it and a person decides
	if st, failed := recordState(t, record); st != "held" || failed != "2.0.0" {
		t.Fatalf("record %s failed %s", st, failed)
	}
	rb := events(l, "launcher_rollback")
	if len(rb) != 1 || rb[0].Detail["reason"] != "boot_loop" || rb[0].Detail["to"] != kept {
		t.Fatalf("launcher_rollback = %+v", rb)
	}
}

// The fallback from a release that added no migration (its start took no dump, and the
// record keeps the release before as the one whose migrations the database holds): the
// kept payload runs at once and the record is not marked, so its start puts nothing back.
func TestBundleGuardWithoutMigrationsPutsNothingBack(t *testing.T) {
	t.Parallel()
	processTest(t)
	bundle, root := mkBundle(t, "crash")
	mkPayload(t, bundle, "2.0.0", "crash", "new")
	kept := filepath.Join(root, "updates", "payloads", "1.0.0")
	mkPayload(t, kept, "1.0.0", "run", "old")
	record := filepath.Join(root, "database.json")
	b, _ := json.Marshal(map[string]any{"version": "1.0.0", "release": "2.0.0", "state": "migrated", "from": "1.0.0"})
	if err := os.WriteFile(record, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := layout.WriteGuard(root, layout.Guard{Version: "2.0.0", RunID: "run-1", Since: time.Now(),
		PreviousPayload: kept, Record: record}); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.Bundle = bundle
	startLauncher(t, cfg)
	l := layout.New(root)
	waitFor(t, "the kept payload runs", func() bool { return countAct(readTrace(l), "old", "run") == 1 })
	if st, failed := recordState(t, record); st != "migrated" || failed != "" {
		t.Fatalf("record %s failed %s: marked for a dump that was never taken", st, failed)
	}
	if ev := events(l, "launcher_record_held"); len(ev) != 0 {
		t.Fatalf("launcher_record_held = %+v", ev)
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
	// an update to 3.0.0 the app has not installed (its install failed, the app started
	// the server again): the kept copy goes on running, and the guard is not its own
	_ = layout.WriteGuard(root, layout.Guard{Version: "3.0.0", Since: time.Now()})
	if p := ln.bundlePayload(); p != kept || ln.isPending(bundleRelease) {
		t.Fatalf("a guard of 3.0.0 with the app on 2.0.0: runs %q, pending %v", p, ln.isPending(bundleRelease))
	}
	// the app holds the guarded release: it runs, under the guard, over the override
	_ = layout.WriteGuard(root, layout.Guard{Version: "2.0.0", Since: time.Now()})
	if p := ln.bundlePayload(); p != bundle || !ln.isPending(bundleRelease) {
		t.Fatalf("a guard of the app's 2.0.0: runs %q, pending %v", p, ln.isPending(bundleRelease))
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
	if err := l.GuardPending("2.0.0", time.Now().Add(time.Hour), 0, record); err != nil {
		t.Fatal(err)
	}
	startLauncher(t, testConfig(l.Root))
	waitFor(t, "previous started", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	// held, never restoring: the kept payload stops on it and a person decides
	if st, failed := recordState(t, record); st != "held" || failed != "2.0.0" {
		t.Fatalf("record %s failed %s", st, failed)
	}
}

// guardTime is a guard whose clock and running time the test moves: a release with budget to
// confirm, its deadline pushed as ExtendGuard pushes it (never back).
type guardTime struct {
	c        guardClock
	wall     time.Time
	tick     time.Duration
	deadline time.Time
	budget   time.Duration
	timeout  time.Duration
	used     time.Duration // what a start before used, as saved
}

func newGuardTime(budget, deadline time.Duration) *guardTime {
	g := &guardTime{wall: time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC), budget: budget, timeout: 15 * time.Minute}
	if deadline > 0 {
		g.deadline = g.wall.Add(deadline)
	}
	return g
}

// restart is the launcher started again (the machine was off, or the launcher was).
func (g *guardTime) restart() { g.used, g.tick, g.c = g.c.ran, 0, guardClock{} }

// runs lets d pass with the launcher running, a look every 5 s, the release pushing its
// deadline 15 minutes ahead every minute (push); how long it took to be given up on (0: not).
func (g *guardTime) runs(d time.Duration, push bool) time.Duration {
	const look = 5 * time.Second
	for t := time.Duration(0); t < d; t += look {
		g.wall, g.tick = g.wall.Add(look), g.tick+look
		if until := g.wall.Add(15 * time.Minute); push && t%time.Minute == 0 && g.deadline.Before(until) {
			g.deadline = until
		}
		if g.c.look(g.tick, g.wall, "2.0.0 run-1", g.deadline, g.budget, g.timeout, func(string) time.Duration { return g.used }) {
			return t + look
		}
	}
	return 0
}

// The guard's time is the time the release ran (the review of #1607). A good release is not
// given up on after a long power-off, a sleep, or a clock set forward; one that hung is,
// whatever it pushes and wherever the clock is set; a rollback under way has its own time.
func TestGuardCountsRunningTime(t *testing.T) {
	t.Parallel()
	const budget = 3 * time.Hour
	mk := func() *guardTime { return newGuardTime(budget, 40*time.Minute) }
	stays := func(t *testing.T, g *guardTime, what string, d time.Duration) {
		t.Helper()
		if at := g.runs(d, true); at != 0 {
			t.Fatalf("%s: given up on after %s", what, at)
		}
	}
	ends := func(t *testing.T, what string, got, want time.Duration) {
		t.Helper()
		if got < want-time.Minute || got > want+time.Minute {
			t.Errorf("%s: given up on after %s, want %s", what, got, want)
		}
	}
	t.Run("a 5 h power-off mid-watch", func(t *testing.T) {
		g := mk()
		stays(t, g, "before it", 10*time.Minute)
		g.wall = g.wall.Add(5 * time.Hour)
		g.restart()
		stays(t, g, "after the boot", 2*time.Hour)
	})
	t.Run("a clock set forward", func(t *testing.T) {
		g := mk()
		stays(t, g, "before it", 5*time.Minute)
		g.wall = g.wall.Add(50 * 365 * 24 * time.Hour) // a NAS with no clock of its own, then NTP
		stays(t, g, "after it", 2*time.Hour)
	})
	t.Run("a clock set forward, the push a minute late", func(t *testing.T) {
		g := mk()
		stays(t, g, "before it", 5*time.Minute)
		g.wall = g.wall.Add(365 * 24 * time.Hour)
		if at := g.runs(10*time.Minute, false); at != 0 {
			t.Fatalf("given up on after %s", at)
		}
	})
	t.Run("a clock set forward, nothing pushed since", func(t *testing.T) {
		g := mk() // 40 minutes: past the 15 of this start
		stays(t, g, "before it", 5*time.Minute)
		g.wall = g.wall.Add(365 * 24 * time.Hour)
		ends(t, "what was left of its deadline", g.runs(2*time.Hour, false), 35*time.Minute)
	})
	t.Run("no update run watches it, its launcher started over and over", func(t *testing.T) {
		g := newGuardTime(time.Hour, 0) // it takes the machine down within its 15 minutes each time
		for range 5 {
			if at := g.runs(11*time.Minute, false); at != 0 {
				t.Fatalf("given up on after %s", at)
			}
			g.restart()
		}
		ends(t, "what is left of the budget", g.runs(2*time.Hour, false), 5*time.Minute)
	})
	t.Run("given up on with nothing to go back to", func(t *testing.T) {
		g := mk()
		ends(t, "the deadline", g.runs(2*time.Hour, false), 40*time.Minute)
		for i := range 9 { // twice as long each time, at most its whole budget
			g.c.again(g.timeout)
			want := min(g.timeout<<min(i, 6), budget)
			ends(t, "once more", g.runs(10*time.Hour, false), want)
		}
		g.c.again(g.timeout)
		stays(t, g, "its budget used, started again", 2*time.Hour)
	})
	t.Run("a sleep, where the tick counts it too", func(t *testing.T) {
		g := mk()
		stays(t, g, "before it", 5*time.Minute)
		g.wall, g.tick = g.wall.Add(9*time.Hour), g.tick+9*time.Hour
		stays(t, g, "after it woke", 2*time.Hour)
	})
	t.Run("the clock set back, healthy", func(t *testing.T) {
		g := mk()
		stays(t, g, "before it", 30*time.Minute)
		g.wall = g.wall.Add(-time.Hour)
		stays(t, g, "after it", 2*time.Hour)
	})
	t.Run("never confirmed, nothing pushed", func(t *testing.T) {
		ends(t, "the deadline", mk().runs(2*time.Hour, false), 40*time.Minute)
	})
	t.Run("no update run watches it", func(t *testing.T) {
		ends(t, "the timeout", newGuardTime(budget, 0).runs(2*time.Hour, false), 15*time.Minute)
	})
	t.Run("the deadline moved back", func(t *testing.T) {
		g := mk()
		stays(t, g, "before it", 20*time.Minute)
		g.deadline = g.wall
		if at := g.runs(time.Minute, false); at == 0 {
			t.Fatal("a deadline moved back was not taken")
		}
	})
	t.Run("hung, pushing for ever", func(t *testing.T) {
		ends(t, "the budget", mk().runs(10*time.Hour, true), budget)
	})
	t.Run("hung, across restarts of the launcher", func(t *testing.T) {
		g := mk()
		for range 5 {
			stays(t, g, "before its budget", 30*time.Minute)
			g.restart()
		}
		ends(t, "what is left of the budget", g.runs(10*time.Hour, true), budget-150*time.Minute)
	})
	t.Run("hung, the clock set back", func(t *testing.T) {
		g := mk()
		g.wall = g.wall.Add(-365 * 24 * time.Hour)
		ends(t, "the budget", g.runs(10*time.Hour, true), budget)
	})
	t.Run("a rollback under way", func(t *testing.T) {
		g := mk()
		stays(t, g, "before its rollback", budget-10*time.Minute)
		g.budget = layout.GuardBudget(int64(budget/time.Second), true)
		ends(t, "the rollback's own time", g.runs(20*time.Hour, true), layout.RollbackRun+10*time.Minute)
	})
}

// The launcher reads the guard's time of each kind (a layout's marker, an app's or a
// runtime's guard), goes on from what its starts before used, and forgets it with the guard.
func TestGuardTimeOfEachKind(t *testing.T) {
	t.Parallel()
	start := time.Now()
	for name, mk := range map[string]func(t *testing.T) *launcher{
		"layout": func(t *testing.T) *launcher {
			l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
			if err := l.Switch("2.0.0", "run-1", start); err != nil {
				t.Fatal(err)
			}
			if err := l.GuardPending("2.0.0", start.Add(time.Hour), 10*time.Minute, ""); err != nil {
				t.Fatal(err)
			}
			return &launcher{cfg: Config{Root: l.Root}, l: l}
		},
		"app": func(t *testing.T) *launcher {
			bundle, root := mkBundle(t, "run")
			mkPayload(t, bundle, "2.0.0", "run", "new")
			if err := layout.WriteGuard(root, layout.Guard{Version: "2.0.0", RunID: "run-1", Since: start,
				Deadline: start.Add(time.Hour), MaxRunSec: 600}); err != nil {
				t.Fatal(err)
			}
			return &launcher{cfg: Config{Root: root, Bundle: bundle}, l: layout.New(root)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ln := mk(t)
			var tick time.Duration
			cfg := ln.cfg
			cfg.Now, cfg.Tick = func() time.Time { return start }, func() time.Duration { return tick }
			for i := range 2 {
				// two starts of the launcher, five minutes each
				ln = &launcher{cfg: cfg, l: ln.l}
				for tick = 0; tick < 5*time.Minute; tick += time.Minute {
					if ln.guardLook("2.0.0") {
						t.Fatalf("start %d: given up on after %s", i+1, tick)
					}
				}
			}
			ln = &launcher{cfg: cfg, l: ln.l}
			up := false
			for tick = 0; tick < 3*time.Minute && !up; tick += time.Minute {
				up = ln.guardLook("2.0.0")
			}
			if !up {
				t.Fatalf("its ten minutes used in two starts, the third gave it %s more", tick)
			}
			key, _, _ := ln.pendingGuard("2.0.0")
			if ln.readGuardRan(key) == 0 || ln.readGuardRan("3.0.0 run-2") != 0 {
				t.Fatal("the time counted is not that guard's alone")
			}
			if name == "app" { // gone back, the next guard's time starts at nothing
				g, _ := layout.ReadGuard(ln.cfg.Root)
				prev := t.TempDir()
				stubPayload(t, prev, "1.0.0")
				g.PreviousPayload = prev
				if err := layout.WriteGuard(ln.cfg.Root, *g); err != nil {
					t.Fatal(err)
				}
			}
			ln.log = newEventLog(filepath.Join(t.TempDir(), "log"), io.Discard, cfg.Now)
			defer ln.log.Close()
			if !ln.rollback("2.0.0", "pending_timeout") {
				t.Fatal("it did not go back")
			}
			if _, err := os.Stat(ln.guardRanPath()); err == nil {
				t.Fatal("the time of a guard that is gone is kept")
			}
		})
	}
	if got := layout.GuardBudget(0, false); got != layout.MaxGuardRun {
		t.Fatalf("a guard from before: %s", got)
	}
	if got := layout.GuardBudget(int64(27*time.Hour/time.Second), false); got != 27*time.Hour {
		t.Fatalf("a large library's: %s", got)
	}
}

// A release the guard gives up on with nothing to go back to (its kept copy gone, a full
// disk) stays up: its time starts again, its budget used up or not. It was stopped at the
// first look of every start, for good.
func TestAReleaseWithNothingToGoBackToStays(t *testing.T) {
	t.Parallel()
	processTest(t)
	for name, g := range map[string]layout.Guard{
		"the deadline passed":   {MaxRunSec: 3 * 3600},
		"its budget is used up": {MaxRunSec: 60, RunID: "used"},
	} {
		t.Run(name, func(t *testing.T) {
			bundle, root := mkBundle(t, "run")
			mkPayload(t, bundle, "2.0.0", "run", "new")
			g.Version, g.Since = "2.0.0", time.Now().Add(-time.Hour)
			g.Deadline, g.PreviousPayload = time.Now().Add(-time.Minute), filepath.Join(root, "gone")
			if err := layout.WriteGuard(root, g); err != nil {
				t.Fatal(err)
			}
			cfg := testConfig(root)
			cfg.Bundle, cfg.PendingTimeout = bundle, time.Second
			l := layout.New(root)
			if g.RunID != "" {
				ln := &launcher{cfg: cfg, l: l}
				key, _, _ := ln.pendingGuard("2.0.0")
				ln.clock = guardClock{key: key, ran: time.Hour}
				ln.saveGuardRan()
			}
			stop := startLauncher(t, cfg)
			waitFor(t, "given up on", func() bool { return len(events(l, "no_previous")) > 0 })
			time.Sleep(5 * time.Second)
			// 1 s, then 1, 2, 4: a few starts, never one a look (20 ms)
			if n := len(events(l, "launcher_pending_timeout")); n > 5 {
				t.Fatalf("given up on %d times in 5 s", n)
			}
			if cur := countAct(readTrace(l), "new", "run"); cur < 2 {
				t.Fatalf("%d starts", cur)
			}
			if err := stop(); err != nil {
				t.Fatal(err)
			}
			// the machine starts again: its time is not used up (this last start's at most; none
			// when the time had just started again)
			ln := &launcher{cfg: cfg, l: l}
			key, _, _ := ln.pendingGuard("2.0.0")
			if ran := ln.readGuardRan(key); ran > 10*time.Second {
				t.Fatalf("%s of its time used, as kept for the next start", ran)
			}
		})
	}
}

// The time a release ran is kept when the launcher stops it (the machine stopped, the service
// restarted), however short its run: a release that hangs uses its budget up across stops.
// Nothing here runs long enough for a look to save it.
func TestGuardTimeIsKeptWhenTheLauncherStops(t *testing.T) {
	t.Parallel()
	processTest(t)
	bundle, root := mkBundle(t, "run")
	mkPayload(t, bundle, "2.0.0", "run", "new")
	if err := layout.WriteGuard(root, layout.Guard{Version: "2.0.0", RunID: "run-1", Since: time.Now(),
		Deadline: time.Now().Add(time.Hour), MaxRunSec: 7200}); err != nil {
		t.Fatal(err)
	}
	l := layout.New(root)
	var want time.Duration
	for start := range 2 {
		var ticks atomic.Int64 // a millisecond a look
		cfg := testConfig(root)
		cfg.Bundle = bundle
		cfg.Tick = func() time.Duration { return time.Duration(ticks.Add(1)) * time.Millisecond }
		stop := startLauncher(t, cfg)
		waitFor(t, "a few looks", func() bool { return ticks.Load() >= 3 && countAct(readTrace(l), "new", "run") == start+1 })
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		want += time.Duration(ticks.Load()) * time.Millisecond
		ln := &launcher{cfg: cfg, l: l}
		key, _, _ := ln.pendingGuard("2.0.0")
		if got := ln.readGuardRan(key); got < want-time.Millisecond/2 || got > want+time.Millisecond/2 {
			t.Fatalf("start %d: %s kept, ran %s", start+1, got, want)
		}
	}
}

// The repro of the review of #1607, with a real server under the launcher: the machine off
// for 5 hours past every deadline, then started: the release runs on and confirms; nothing
// goes back and the record is as it was.
func TestLongPowerOffKeepsAGoodRelease(t *testing.T) {
	t.Parallel()
	processTest(t)
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
	record := filepath.Join(l.Root, "database.json")
	writeRecord(t, record, "2.0.0", "1.0.0")
	off := time.Now().Add(-5 * time.Hour)
	if err := l.Switch("2.0.0", "run-1", off); err != nil {
		t.Fatal(err)
	}
	if err := l.GuardPending("2.0.0", off.Add(20*time.Minute), 3*time.Hour, record); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(l.Root)
	cfg.PendingTimeout = 2 * time.Second
	startLauncher(t, cfg)
	waitFor(t, "the release running", func() bool { return countAct(readTrace(l), "2.0.0", "run") == 1 })
	time.Sleep(500 * time.Millisecond)
	if ok, err := l.ExtendPending("run-1", time.Now().Add(15*time.Minute)); !ok || err != nil {
		t.Fatalf("the release's push: %v %v", ok, err)
	}
	time.Sleep(3 * time.Second)
	if cur, _ := l.Current(); cur != "2.0.0" {
		t.Fatalf("current is %s", cur)
	}
	if n := len(events(l, "launcher_rollback")) + len(events(l, "launcher_pending_timeout")); n != 0 {
		t.Fatalf("%d rollbacks", n)
	}
	if st, _ := recordState(t, record); st != "migrated" {
		t.Fatalf("record %s", st)
	}
}

// A rollback leaves the app's failed release on disk and the kept copy running. The app
// installed again later (by hand, or an update to a newer release) runs, and the
// override goes: on 2026-10-27 a hand install of 0.3.37 went on running the kept 0.3.35
// until the override was removed by hand. The failed release itself, an older app, or an
// override with no record of what failed keep the kept copy.
func TestANewerAppTakesOverFromTheKeptCopy(t *testing.T) {
	t.Parallel()
	// The launcher only chooses a payload here, it starts none: stub servers.
	bundle, root := filepath.Join(t.TempDir(), "Maktaba.app", "Contents", "Resources", "server"), t.TempDir()
	if err := os.MkdirAll(layout.New(root).UpdatesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(root, "updates", "payloads", "0.3.35")
	stubPayload(t, kept, "0.3.35")
	ln := &launcher{cfg: Config{Root: root, Bundle: bundle}, l: layout.New(root), log: newEventLog(layout.New(root).LauncherLog(), nil, time.Now)}
	defer ln.log.Close()
	runs := func(app string) string {
		stubPayload(t, bundle, app)
		return ln.bundlePayload()
	}

	// the rollback of 0.3.36: the app still holds it, the kept copy runs
	if err := layout.WriteRollbackOverride(root, kept, "v0.3.36"); err != nil {
		t.Fatal(err)
	}
	if p := runs("0.3.36"); p != kept {
		t.Fatalf("the app on the failed 0.3.36 runs %q, want the kept copy", p)
	}
	// an app older than the kept copy never takes over
	if p := runs("0.3.34"); p != kept {
		t.Fatalf("an app on 0.3.34 runs %q, want the kept 0.3.35", p)
	}
	// a newer release installed: it runs and the override is gone
	if p := runs("0.3.37"); p != bundle {
		t.Fatalf("an app on 0.3.37 runs %q, want the app's own", p)
	}
	if layout.ReadOverride(root) != "" || layout.ReadOverrideFailed(root) != "" {
		t.Fatalf("override %q failed %q left", layout.ReadOverride(root), layout.ReadOverrideFailed(root))
	}
	if ev := events(ln.l, "launcher_override_superseded"); len(ev) != 1 {
		t.Fatalf("launcher_override_superseded = %+v", ev)
	}

	// an override with no record of the failed release (an older launcher's, or by
	// hand): the app may be the release that failed, so the kept copy goes on running
	if err := layout.WriteOverride(root, kept); err != nil {
		t.Fatal(err)
	}
	if p := runs("0.3.38"); p != kept {
		t.Fatalf("no record of the failed release: runs %q, want the kept copy", p)
	}
}
