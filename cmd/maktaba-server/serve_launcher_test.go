package main

import (
	"context"
	"os"
	"path/filepath"
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
