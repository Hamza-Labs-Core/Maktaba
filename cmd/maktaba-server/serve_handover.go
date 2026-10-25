package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// The TV handover across a supervisor restart (Epic 35 story 35.7, design §8.1), as
// serve wires it: the supervisor keeps the record of the TV processes in the launcher's
// updates directory (supervisor.WithHandover), and an update's restart (an exit 75 to
// the launcher) hands them over when they can run on across it: they keep serving while
// every other process restarts on the new release, and the next supervisor adopts them.
// The run then need not wait for the TV to be quiet (the home's driver reads hands_over).
// Where they cannot (another install kind, a launcher from before it, a front door the
// new release does not speak to), every process restarts, under the run's TV gate.

// handoverDir is where the supervisor keeps the record: the layout's updates directory
// when a launcher runs a release of it (a plain server, whose update restarts are exits
// 75 to that launcher); "" for every other install (the desktop apps install their own
// way and restart whole).
func handoverDir(releaseDir string) string {
	if releaseDir == "" || os.Getenv(layout.EnvLauncher) != "1" {
		return ""
	}
	return layout.New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(releaseDir))))).UpdatesDir()
}

// launcherPID is the launcher that started this server (its parent), 0 when none did.
func launcherPID() int {
	if os.Getenv(layout.EnvLauncher) != "1" {
		return 0
	}
	return os.Getppid()
}

// tvHandover is the update machine's TVHandover: the TV processes run on across the
// restart onto target when this is a layout install, the launcher keeps them (on Linux
// it must say so: an older one stops what the server leaves behind), the supervisor can
// hand them over (Windows: the launcher holds their job), and the front door's program
// speaks target's edge protocol (design §7.3: otherwise it restarts). arm makes the next
// exit 75 hand them over (beforeExit).
func (u *updates) tvHandover(_ *updater.Journal, target string, arm bool) (bool, string) {
	u.mu.Lock()
	sup, lay := u.sup, u.lay
	u.mu.Unlock()
	if lay == nil {
		return false, "this install restarts whole"
	}
	if sup == nil {
		return false, "the supervisor does not run yet"
	}
	if p := layout.ReleaseHandoverProtocol(lay.ReleaseDir(layout.NormalizeVersion(target))); p < 1 {
		// a release from before the handover adopts nothing: it would start its own TV
		// processes on the ports the handed-over ones hold
		return false, layout.NormalizeVersion(target) + "'s supervisor does not adopt the TV processes (its release.json says no handover_protocol)"
	}
	if goruntime.GOOS == "linux" && os.Getenv(layout.EnvLauncherHandover) != "1" {
		return false, "the launcher is from before the TV handover (the committed release's takes its place)"
	}
	if fd := sup.SurvivorRelease(supervisor.RoleStreaming); fd != "" {
		have := edgeProtocol(lay.ReleaseDir(fd))
		want := edgeProtocol(lay.ReleaseDir(layout.NormalizeVersion(target)))
		if updater.FrontDoorRestarts(want, have) {
			return false, fmt.Sprintf("the TV front door (%s) speaks edge protocol %d and %s %d: it restarts", fd, have,
				layout.NormalizeVersion(target), want)
		}
	}
	ok, why := sup.CanHandOver()
	if ok && arm {
		u.armed.Store(true)
	}
	return ok, why
}

// beforeExit runs as an update asks serve to exit: the exit 75 tvHandover armed hands
// the TV processes over (the supervisor's Run then ends without stopping them).
func (u *updates) beforeExit(code int) {
	if code != 75 || !u.armed.Swap(false) {
		return
	}
	u.mu.Lock()
	sup := u.sup
	u.mu.Unlock()
	if sup == nil {
		return
	}
	if ok, why := sup.HandOver(); !ok {
		fmt.Fprintf(os.Stderr, "[serve] updates: the TV processes cannot be handed over (%s): every process restarts\n", why)
		return
	}
	fmt.Fprintln(os.Stderr, "[serve] updates: the TV processes run on across the restart")
}

// edgeProtocol is the edge_protocol of the release in dir (its release.json), 0 when it
// cannot be read.
func edgeProtocol(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, releaseverify.ManifestFile))
	if err != nil {
		return 0
	}
	var m releaseverify.Manifest
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	return m.EdgeProtocol
}
