package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

func TestLauncherReleaseRefusesOutsideLayout(t *testing.T) {
	t.Setenv(layout.EnvLauncher, "")
	t.Setenv(layout.EnvReleaseDir, "")
	if rd, err := launcherRelease(); err != nil || rd != "" {
		t.Fatalf("not under the launcher: %q %v", rd, err)
	}
	t.Setenv(layout.EnvLauncher, "1")
	if _, err := launcherRelease(); err == nil {
		t.Fatal("under the launcher without MAKTABA_RELEASE_DIR")
	}
	t.Setenv(layout.EnvReleaseDir, t.TempDir()) // not where this binary is
	if _, err := launcherRelease(); err == nil {
		t.Fatal("a release dir that does not hold this executable was accepted")
	}
	self, _ := os.Executable()
	t.Setenv(layout.EnvReleaseDir, filepath.Dir(self))
	if rd, err := launcherRelease(); err != nil || rd != filepath.Dir(self) {
		t.Fatalf("own dir: %q %v", rd, err)
	}
}

// The database's record names its update's backup once the release runs healthy and nothing
// would still take it back: not while the launcher's guard, a layout's pending marker or an
// update run names it, nor for another release's record.
func TestConfirmRecordOnceNothingTakesTheReleaseBack(t *testing.T) {
	spawnsPrograms(t) // a real child under the supervisor
	t.Setenv("MAKTABA_CONFIRM_AFTER", "1ms")
	prev := confirmRecordEvery
	t.Cleanup(func() { confirmRecordEvery = prev })
	confirmRecordEvery = 5 * time.Millisecond
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self)
	child.Env = append(os.Environ(), "MAKTABA_TEST_APPBUNDLE_CHILD=1")
	sup := supervisor.New([]supervisor.Process{{Role: supervisor.RoleAPI, Cmd: child}}, supervisor.DefaultPolicy(),
		supervisor.WithLog(&strings.Builder{}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, 5*time.Second) }()
	defer func() {
		cancel()
		<-done
	}()
	dir := t.TempDir()
	path := filepath.Join(dir, "database.json")
	read := func() string { b, _ := os.ReadFile(path); return string(b) }
	if err := os.WriteFile(path, []byte(`{"version":"v0.3.0","state":"migrated","from":"v0.2.0","dump_unconfirmed":"/b/d.dump"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var tried atomic.Bool
	tried.Store(true)
	go confirmRecord(ctx, sup, path, "v0.3.0", tried.Load)
	waitUntil(t, "the API running", func() bool { return apiRanFor(sup.Snapshot(), time.Millisecond) })
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(read(), "dump_unconfirmed") {
		t.Fatalf("named while the release may still be taken back: %s", read())
	}
	tried.Store(false)
	waitUntil(t, "the record naming its backup", func() bool { return strings.Contains(read(), `"dump": "/b/d.dump"`) })
	if strings.Contains(read(), "dump_unconfirmed") {
		t.Fatalf("record %s", read())
	}
}

// The start's own wiring: the release that runs is the record's, and it is named once nothing
// takes it back. (cmd_serve.go calls it: TestServeStartsTheConfirms.)
func TestStartConfirmsNamesTheRunningReleasesBackup(t *testing.T) {
	spawnsPrograms(t)
	t.Setenv("MAKTABA_CONFIRM_AFTER", "1ms")
	root := t.TempDir()
	t.Setenv(envLauncherRoot, root)
	prev := confirmRecordEvery
	t.Cleanup(func() { confirmRecordEvery = prev })
	confirmRecordEvery = 5 * time.Millisecond
	sup, stop := runningAPI(t)
	defer stop()
	releaseDir := filepath.Join(t.TempDir(), "bin", "releases", "0.3.0")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "database.json")
	if err := os.WriteFile(path, []byte(`{"version":"v0.3.0","state":"migrated","from":"v0.2.0","dump_unconfirmed":"/b/d.dump"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := layout.WriteGuard(root, layout.Guard{Version: "0.3.0", RunID: "r", Since: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var asked atomic.Int32
	startConfirms(ctx, sup, path, "", releaseDir, func() *updater.Journal { asked.Add(1); return nil })
	waitUntil(t, "the run asked for", func() bool { return asked.Load() > 2 })
	read := func() string { b, _ := os.ReadFile(path); return string(b) }
	if !strings.Contains(read(), "dump_unconfirmed") {
		t.Fatalf("named while the launcher's guard names it: %s", read())
	}
	if err := layout.ClearGuard(root); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the record naming its backup", func() bool { return strings.Contains(read(), `"dump": "/b/d.dump"`) })
}

func TestServeStartsTheConfirms(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "cmd_serve.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "startConfirms" {
				called = true
			}
		}
		return true
	})
	if !called {
		t.Fatal("serve starts nothing that confirms the running release")
	}
}

// Nothing is named before the API ran: until then the database step may still hold the update.
func TestConfirmRecordWaitsForTheAPI(t *testing.T) {
	spawnsPrograms(t)
	t.Setenv("MAKTABA_CONFIRM_AFTER", "1ms")
	prev := confirmRecordEvery
	t.Cleanup(func() { confirmRecordEvery = prev })
	confirmRecordEvery = time.Millisecond
	path := filepath.Join(t.TempDir(), "database.json")
	const rec = `{"version":"v0.3.0","state":"migrated","from":"v0.2.0","dump_unconfirmed":"/b/d.dump"}`
	if err := os.WriteFile(path, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// an API that has not started
	sup := supervisor.New([]supervisor.Process{{Role: supervisor.RoleAPI, Cmd: exec.Command("never")}}, supervisor.DefaultPolicy(),
		supervisor.WithLog(&strings.Builder{}))
	confirmRecord(ctx, sup, path, "v0.3.0", func() bool { return false })
	if b, _ := os.ReadFile(path); string(b) != rec {
		t.Fatalf("named before the API ran: %s", b)
	}
}

// runningAPI is a supervisor whose API runs (a real child); the func stops it.
func runningAPI(t *testing.T) (*supervisor.Supervisor, func()) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self)
	child.Env = append(os.Environ(), "MAKTABA_TEST_APPBUNDLE_CHILD=1")
	sup := supervisor.New([]supervisor.Process{{Role: supervisor.RoleAPI, Cmd: child}}, supervisor.DefaultPolicy(),
		supervisor.WithLog(&strings.Builder{}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, 5*time.Second) }()
	waitUntil(t, "the API running", func() bool { return apiRanFor(sup.Snapshot(), time.Millisecond) })
	return sup, func() {
		cancel()
		<-done
	}
}

func TestAPIRanFor(t *testing.T) {
	api := supervisor.ChildStatus{Role: supervisor.RoleAPI, Wanted: true, State: supervisor.StateRunning, UptimeSec: 200, Restarts: 2}
	young, down, other := api, api, api
	young.UptimeSec, down.State, other.Role = 30, supervisor.StateBackoff, supervisor.RoleStreaming
	for name, c := range map[string]struct {
		p    supervisor.ChildStatus
		want bool
	}{"ran, restarted before": {api, true}, "young": {young, false}, "down": {down, false}, "another role": {other, false}} {
		if got := apiRanFor(supervisor.Snapshot{Processes: []supervisor.ChildStatus{c.p}}, 3*time.Minute); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestReleaseTried(t *testing.T) {
	root := t.TempDir()
	if releaseTried(root, "", "v1.5.0", nil) {
		t.Error("nothing names it")
	}
	for v, want := range map[string]bool{"1.5.0": true, "1.4.0": false} {
		if err := layout.WriteGuard(root, layout.Guard{Version: v, RunID: "r", Since: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if got := releaseTried(root, "", "v1.5.0", nil); got != want {
			t.Errorf("the guard names %s: %v", v, got)
		}
	}
	if err := os.WriteFile(layout.GuardPath(root), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !releaseTried(root, "", "v1.5.0", nil) {
		t.Error("a guard that cannot be read")
	}
	// a layout install's pending marker
	in := t.TempDir()
	rel := filepath.Join(in, "bin", "releases", "1.5.0")
	if releaseTried("", rel, "1.5.0", nil) {
		t.Error("no pending marker")
	}
	if err := os.WriteFile(filepath.Join(in, layout.PendingFile), []byte(`{"version":"1.5.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !releaseTried("", rel, "1.5.0", nil) || releaseTried("", rel, "1.4.0", nil) {
		t.Error("the pending marker's release")
	}
	// an update run of it under way, then ended
	for state, want := range map[string]bool{updater.StateWatching: true, updater.StateRestarting: true, updater.StateCommitted: false, updater.StateRolledBack: false} {
		run := &updater.Journal{Job: updater.Job{RunID: "run-1", Version: "1.5.0"}, State: state}
		if got := releaseTried("", "", "v1.5.0", run); got != want {
			t.Errorf("a run %s: %v, want %v", state, got, want)
		}
	}
}

func TestWatchStopFile(t *testing.T) {
	stopFilePoll = 10 * time.Millisecond
	stop := filepath.Join(t.TempDir(), "stop-1")
	t.Setenv(envStopFile, stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchLauncher(ctx, cancel, "")
	_ = os.WriteFile(stop, []byte("stop"), 0o644)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the stop file did not stop serve")
	}
}

func TestHealthyFor(t *testing.T) {
	ok := supervisor.ChildStatus{Wanted: true, State: supervisor.StateRunning, UptimeSec: 200}
	idle := supervisor.ChildStatus{Wanted: false, State: supervisor.StateExited} // on-demand, not asked for
	if !healthyFor(supervisor.Snapshot{Processes: []supervisor.ChildStatus{ok, idle}}, 3*time.Minute) {
		t.Fatal("healthy roles not confirmed")
	}
	young := ok
	young.UptimeSec = 30
	restarted := ok
	restarted.Restarts = 1
	for name, c := range map[string]supervisor.ChildStatus{"young": young, "restarted": restarted,
		"backoff": {Wanted: true, State: supervisor.StateBackoff, UptimeSec: 500}} {
		if healthyFor(supervisor.Snapshot{Processes: []supervisor.ChildStatus{ok, c}}, 3*time.Minute) {
			t.Errorf("%s: confirmed", name)
		}
	}
	if healthyFor(supervisor.Snapshot{}, time.Minute) {
		t.Error("nothing running counts as healthy")
	}
}

// An app's launcher (or a runtime copy's) sets no release directory, only its state root:
// a restart request written there (by hand after a payload-override, 2026-10-28) is taken
// all the same, and serve exits 75 for the launcher.
func TestRestartRequestUnderAnAppLauncher(t *testing.T) {
	stopFilePoll = 10 * time.Millisecond
	t.Setenv(envStopFile, "")
	root := t.TempDir()
	t.Setenv(envLauncherRoot, root)
	l := layout.New(root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restart := watchLauncher(ctx, cancel, "")
	if err := os.MkdirAll(l.UpdatesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.RestartRequestPath(), []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a restart request under an app's launcher did not stop serve")
	}
	if !restart.Load() {
		t.Fatal("restart not reported")
	}
	if _, err := os.Stat(l.RestartRequestPath()); err == nil {
		t.Fatal("the request was not consumed")
	}
}

func TestRestartRequestExits75(t *testing.T) {
	stopFilePoll = 10 * time.Millisecond
	t.Setenv(envStopFile, "")
	root := t.TempDir()
	l := layout.New(root)
	rel := l.ReleaseDir("1.5.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restart := watchLauncher(ctx, cancel, rel)
	if err := l.RequestRestart(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a restart request did not stop serve")
	}
	if !restart.Load() {
		t.Fatal("restart not reported")
	}
	if _, err := os.Stat(l.RestartRequestPath()); err == nil {
		t.Fatal("the request was not consumed")
	}
}

// An on-demand process nobody asked for (a first-party plugin runner, idle or held) is never
// waited on: not by a release's confirmation (healthyFor) nor by an adoption, which waits on
// the same check. One that was asked for counts like any other.
func TestHealthyForIgnoresAnOnDemandProcessNotAskedFor(t *testing.T) {
	ok := supervisor.ChildStatus{Wanted: true, State: supervisor.StateRunning, UptimeSec: 200,
		StartMode: supervisor.StartAlways}
	idle := supervisor.ChildStatus{Role: supervisor.RolePluginRunner, Instance: "light",
		StartMode: supervisor.StartOnDemand, State: supervisor.StateStopped}
	held := supervisor.ChildStatus{Role: supervisor.RolePluginRunner, Instance: "service",
		StartMode: supervisor.StartOnDemand, State: supervisor.StateHeld, Blocked: true}
	if !healthyFor(supervisor.Snapshot{Processes: []supervisor.ChildStatus{ok, idle, held}}, time.Minute) {
		t.Fatal("an idle or held on-demand runner kept the release from being confirmed")
	}
	asked := idle
	asked.Wanted, asked.State = true, supervisor.StateStarting
	if healthyFor(supervisor.Snapshot{Processes: []supervisor.ChildStatus{ok, asked}}, time.Minute) {
		t.Fatal("a runner asked for and still starting counted as healthy")
	}
}

// Research P4: `maktaba-server update` on a layout install switched with the run id "cli",
// which no update run watches and confirmPending never confirmed, so the launcher went
// back after 15 minutes. Such a switch confirms itself once healthy, as a switch by hand
// does; an update run's switch is its watch's to confirm.
func TestACommandLineSwitchConfirmsItself(t *testing.T) {
	for _, c := range []struct {
		run  string
		want bool
	}{{"", true}, {cliRunID, true}, {"0d6c1f0e-run", false}} {
		if got := confirmsItself(&layout.Pending{Version: "1.5.0", RunID: c.run}); got != c.want {
			t.Errorf("run id %q: confirms itself %v, want %v", c.run, got, c.want)
		}
	}
	if confirmsItself(nil) {
		t.Error("no marker confirms itself")
	}
}

// Confirming a release raises the watermark before the pending marker goes: one that cannot
// be written leaves the marker, so the release is confirmed again (and the watermark never
// stays behind).
func TestConfirmReleaseRaisesTheWatermarkFirst(t *testing.T) {
	root := t.TempDir()
	l := layout.New(root)
	pend := func() {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, layout.PendingFile), []byte(`{"version":"1.5.0","previous":"1.4.0"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pend()
	// the watermark's place is taken by a folder: it cannot be written
	blocked := filepath.Join(root, layout.WatermarkFile)
	if err := os.MkdirAll(filepath.Join(blocked, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := confirmRelease(l, "1.5.0"); err == nil {
		t.Fatal("confirmed with no watermark")
	}
	if p, _ := l.ReadPending(); p == nil || p.Version != "1.5.0" {
		t.Fatalf("the marker went before the watermark: %+v", p)
	}
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	if err := confirmRelease(l, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.ReadPending(); p != nil || l.Watermark() != "1.5.0" {
		t.Fatalf("pending %+v watermark %q", p, l.Watermark())
	}
	// a stop between the watermark and the marker: confirmed again at the next start
	pend()
	if err := confirmRelease(l, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.ReadPending(); p != nil {
		t.Fatalf("pending %+v", p)
	}
}
