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
	stop := filepath.Join(t.TempDir(), "stop-1")
	t.Setenv(envStopFile, stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchStopFile(ctx, cancel)
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
