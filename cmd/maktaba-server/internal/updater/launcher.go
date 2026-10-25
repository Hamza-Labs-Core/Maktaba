package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Story 35.7 AC6: the launcher is replaced when a release needs a newer one
// (release.json's launcher_protocol above the running launcher's, which the launcher
// passes in MAKTABA_LAUNCHER_PROTOCOL). After the release is committed the server puts
// the new launcher in place and asks the running one to hand over at the next exit 75
// (layout.ReplaceLauncherFile); a release that needs a launcher this install cannot
// replace (one a package manager owns, a Windows service's, which cannot run another in
// its place) is refused before it installs (launcher_protocol: a person updates it).

// LauncherReplacer is an installer whose launcher a release may replace.
type LauncherReplacer interface {
	// ReplaceLauncher puts the release's launcher in place and asks the running one to
	// hand over; false when nothing was to replace.
	ReplaceLauncher(j *Journal) (bool, error)
}

// runningLauncherProtocol is the running launcher's protocol (1 for a launcher from
// before it said: the first protocol).
func runningLauncherProtocol() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(layout.EnvLauncherProtocol))); err == nil && n > 0 {
		return n
	}
	return 1
}

// needsNewLauncher reports whether the job's release needs a newer launcher.
func needsNewLauncher(j *Journal) bool {
	return j.LauncherProtocol > runningLauncherProtocol()
}

// refreshLauncher reports whether the running launcher is from before the TV handover
// (story 35.7: it does not say MAKTABA_LAUNCHER_HANDOVER) where that matters, POSIX: on
// Linux it would stop the TV processes a server hands over, on a Mac it would not stop
// them at a service stop. The committed release's launcher (this server's own build,
// which speaks it) then takes its place where that happens by itself, never asked of a
// person: a launcher that cannot be replaced here stays, and every restart stays whole.
// Windows keeps any launcher (the handover works under the one it has).
func refreshLauncher() bool {
	return runtime.GOOS != "windows" && os.Getenv(layout.EnvLauncherExe) != "" &&
		os.Getenv(layout.EnvLauncherHandover) != "1"
}

// wantsNewLauncher reports whether the job's committed release replaces the running
// launcher: it needs a newer one, or the running one is from before the handover.
func wantsNewLauncher(j *Journal) bool { return needsNewLauncher(j) || refreshLauncher() }

// launcherPackaged reports whether a package manager owns the launcher: a .deb/.rpm's
// (story 35.7 EC4), or Homebrew's (its formula runs the launcher from its own keg, under
// /opt/homebrew or /usr/local).
func launcherPackaged(exe string) bool {
	p := filepath.ToSlash(exe)
	return runtime.GOOS != "windows" &&
		(strings.HasPrefix(p, "/usr/") || strings.HasPrefix(p, "/opt/homebrew/") || strings.Contains(p, "/Cellar/"))
}

// packagedFix is what brings a package manager's launcher up to version: the release's own
// package, or an upgrade from the package repository or the tap where the machine has one.
func packagedFix(exe, version string) string {
	p := filepath.ToSlash(exe)
	if runtime.GOOS == "darwin" || strings.HasPrefix(p, "/opt/homebrew/") || strings.Contains(p, "/Cellar/") {
		return "Homebrew owns it: `brew upgrade maktaba`, then retry the update"
	}
	return fmt.Sprintf("a package manager owns it: install the release's package (%s's `.deb` or `.rpm`: "+
		"`sudo apt install ./maktaba-server_*.deb`, `sudo dnf install ./maktaba-server-*.rpm`), or upgrade it "+
		"from the package repository where the machine has one (`sudo apt install --only-upgrade maktaba-server`, "+
		"`sudo dnf upgrade maktaba-server`), then retry the update", layout.NormalizeVersion(version))
}

// checkLauncher refuses a release whose launcher protocol the running launcher cannot
// be brought to here (canReplace false).
func checkLauncher(j *Journal, canReplace bool, why string) error {
	if !needsNewLauncher(j) || canReplace {
		return nil
	}
	return Fail("launcher_protocol", "%s needs launcher protocol %d and this machine's launcher speaks %d; %s",
		j.Job.Version, j.LauncherProtocol, runningLauncherProtocol(), why)
}

// askHandover writes the request the running launcher takes at the server's next exit 75.
func askHandover(root, next string) error {
	return layout.WriteFileAtomic(filepath.Join(root, "updates", layout.ReplaceLauncherFile), []byte(next+"\n"), 0o644)
}

// layoutLauncherReplaceable: the plain server install's launcher is the layout's own
// file, not a package's, and the OS can hand over in place. version is the release that
// needs it (the refusal names the command that puts its launcher in place by hand).
func (li *LayoutInstaller) launcherReplaceable(version string) (bool, string) {
	exe := strings.TrimSpace(os.Getenv(layout.EnvLauncherExe))
	switch {
	case exe == "":
		return false, "the launcher does not say where it is: update it once by hand (`maktaba-server setup --self-managed`)"
	case launcherPackaged(exe):
		return false, packagedFix(exe, version)
	case runtime.GOOS == "windows":
		next := filepath.Join(li.Layout.ReleaseDir(layout.NormalizeVersion(version)), layout.Exe("maktaba-launcher"))
		return false, fmt.Sprintf("on Windows the running launcher cannot hand over in place: put the release's in place "+
			"and restart Maktaba, in an administrator PowerShell: `& \"%s\" --replace-self \"%s\"; Restart-Service %s` "+
			"(for the sign-in task instead of the service: sign out and in again), then retry the update",
			next, exe, windowsServiceName)
	}
	return true, ""
}

// windowsServiceName is the Windows service (and sign-in task) a plain server runs as
// (internal/service.WindowsName).
const windowsServiceName = "Maktaba"

// ReplaceLauncher implements LauncherReplacer: the release's launcher copied beside the
// running one and moved into place by itself (--replace-self keeps the old as .old,
// removed at the next good start), then the handover asked.
func (li *LayoutInstaller) ReplaceLauncher(j *Journal) (bool, error) {
	if !wantsNewLauncher(j) {
		return false, nil
	}
	if ok, why := li.launcherReplaceable(j.Job.Version); !ok {
		if !needsNewLauncher(j) {
			return false, nil // a refresh only: the launcher this install has stays
		}
		return false, fmt.Errorf("%s", why)
	}
	src := filepath.Join(li.Layout.ReleaseDir(layout.NormalizeVersion(j.Job.Version)), layout.Exe("maktaba-launcher"))
	if _, err := os.Stat(src); err != nil && !needsNewLauncher(j) {
		return false, nil // a release without its own launcher: nothing to refresh with
	}
	exe := strings.TrimSpace(os.Getenv(layout.EnvLauncherExe))
	tmp := exe + ".new"
	if err := layout.CopyFile(src, tmp); err != nil {
		return false, err
	}
	_ = os.Chmod(tmp, 0o755)
	if err := replaceSelf(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, askHandover(li.Layout.Root, exe)
}

// replaceSelf runs the new launcher's --replace-self (tests replace it).
var replaceSelf = func(newLauncher, target string) error {
	if out, err := exec.CommandContext(context.Background(), newLauncher, "--replace-self", target).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ReplaceLauncher implements LauncherReplacer: the runtime's current launcher is the new
// runtime's already; the running one hands over to it.
func (ri *RuntimeInstaller) ReplaceLauncher(j *Journal) (bool, error) {
	if !wantsNewLauncher(j) || ri.LauncherRoot == "" {
		return false, nil
	}
	cur, err := ri.Runtime.Current()
	if err != nil || cur == "" {
		return false, fmt.Errorf("no current runtime: %v", err)
	}
	return true, askHandover(ri.LauncherRoot, filepath.Join(ri.Runtime.Root, cur, "bin", layout.Exe("maktaba-launcher")))
}
