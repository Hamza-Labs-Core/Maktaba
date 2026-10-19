package main

// `serve` under maktaba-launcher (Epic 35, story 35.2): the release directory
// the roles start from, the launcher's graceful-stop file, and confirming a
// pending release once it runs healthy.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// envStopFile is set by the launcher: the file it creates to ask us to stop
// (Windows, where a console-less process has no SIGTERM).
const envStopFile = "MAKTABA_STOP_FILE"

// launcherRelease returns the release directory the launcher started us
// from ("" when not under the launcher). Under the launcher it must be set
// and must hold this executable (AC2: refuse to run outside the layout).
func launcherRelease() (string, error) {
	rd := strings.TrimSpace(os.Getenv(layout.EnvReleaseDir))
	if os.Getenv(layout.EnvLauncher) != "1" {
		return rd, nil
	}
	if rd == "" {
		return "", errors.New("started by maktaba-launcher without MAKTABA_RELEASE_DIR")
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	want := rd
	if resolved, err := filepath.EvalSymlinks(rd); err == nil {
		want = resolved
	}
	if !strings.EqualFold(filepath.Clean(filepath.Dir(self)), filepath.Clean(want)) {
		return "", fmt.Errorf("maktaba-launcher started %s, which is not in the release directory %s", self, rd)
	}
	return rd, nil
}

// watchStopFile cancels when the launcher's stop file appears.
func watchStopFile(ctx context.Context, cancel context.CancelFunc) {
	p := os.Getenv(envStopFile)
	if p == "" {
		return
	}
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := os.Stat(p); err == nil {
					fmt.Fprintln(os.Stderr, "[serve] stop requested by the launcher")
					cancel()
					return
				}
			}
		}
	}()
}

// confirmAfter is how long every role must run without a restart before a
// pending release is confirmed. Until the update orchestrator (story 35.4)
// confirms at its commit step, this is what confirms a release a package
// upgrade or `setup --self-managed` switched to. MAKTABA_CONFIRM_AFTER
// overrides it ("0" turns self-confirmation off: the orchestrator's job).
func confirmAfter() time.Duration {
	if v := os.Getenv("MAKTABA_CONFIRM_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 3 * time.Minute
}

// healthyFor reports whether every always-on process has been running for
// at least d with no restart.
func healthyFor(snap supervisor.Snapshot, d time.Duration) bool {
	n := 0
	for _, p := range snap.Processes {
		if !p.Wanted {
			continue // an on-demand process not asked for
		}
		n++
		if p.State != supervisor.StateRunning || p.Restarts > 0 || p.UptimeSec < d.Seconds() {
			return false
		}
	}
	return n > 0
}

// confirmPending removes the launcher's pending marker (and raises the
// watermark) once this release has run healthy for confirmAfter.
func confirmPending(ctx context.Context, sup *supervisor.Supervisor, releaseDir string) {
	after := confirmAfter()
	if releaseDir == "" || after <= 0 {
		return
	}
	ver := filepath.Base(filepath.Clean(releaseDir))
	l := layout.New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(releaseDir)))))
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			p, err := l.ReadPending()
			if err != nil || p == nil || p.Version != ver {
				return // nothing to confirm (or another version is pending)
			}
			if healthyFor(sup.Snapshot(), after) {
				if err := l.Confirm(); err != nil {
					fmt.Fprintf(os.Stderr, "[serve] confirming release %s failed: %v\n", ver, err)
					return
				}
				_ = l.RaiseWatermark(ver)
				fmt.Fprintf(os.Stderr, "[serve] release %s confirmed (healthy for %s)\n", ver, after)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
