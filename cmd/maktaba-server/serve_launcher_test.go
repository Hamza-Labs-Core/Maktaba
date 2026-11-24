package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
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
// stays behind); the database's record names its backup once it is confirmed.
func TestConfirmReleaseRaisesTheWatermarkFirst(t *testing.T) {
	root := t.TempDir()
	l := layout.New(root)
	record := filepath.Join(root, "state", "database.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	pend := func() {
		t.Helper()
		if err := os.WriteFile(record, []byte(`{"version":"1.5.0","state":"migrated","dump_unconfirmed":"/b/d.dump"}`), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if err := confirmRelease(l, "1.5.0", record); err == nil {
		t.Fatal("confirmed with no watermark")
	}
	if p, _ := l.ReadPending(); p == nil || p.Version != "1.5.0" {
		t.Fatalf("the marker went before the watermark: %+v", p)
	}
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), "dump_unconfirmed") {
		t.Fatalf("the record was confirmed: %s", b)
	}
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	if err := confirmRelease(l, "1.5.0", record); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.ReadPending(); p != nil || l.Watermark() != "1.5.0" {
		t.Fatalf("pending %+v watermark %q", p, l.Watermark())
	}
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), `"dump": "/b/d.dump"`) {
		t.Fatalf("record %s", b)
	}
	// a stop between the watermark and the marker: confirmed again at the next start
	pend()
	if err := confirmRelease(l, "1.5.0", record); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.ReadPending(); p != nil {
		t.Fatalf("pending %+v", p)
	}
}
