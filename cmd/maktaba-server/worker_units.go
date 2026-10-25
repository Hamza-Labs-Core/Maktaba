package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// workerUnits are the Epic 31 installer's worker units on this machine (its pipeline
// worker from before it was a full node): the maktaba-node and maktaba-node@N scheduled
// tasks on Windows, com.maktaba.node*.plist LaunchAgents on a Mac, maktaba-node*.service
// systemd units (the user's, and the system's for a root install) on Linux. `setup --node`
// removes them once the new service answers, and a full node's start removes what a
// conversion left (leftoverWorkerUnits).
type workerUnits struct {
	goos, home string
	// systemUnits is the system's systemd folder, for a root install ("" otherwise)
	systemUnits string
	run         func(name string, args ...string) error
	say         func(format string, a ...any)
}

type unitDir struct {
	dir  string
	user bool
}

// unitDirs are the systemd folders the Epic 31 installer writes the worker's units to.
func (u workerUnits) unitDirs() []unitDir {
	dirs := []unitDir{{dir: filepath.Join(u.home, ".config", "systemd", "user"), user: true}}
	if u.systemUnits != "" {
		dirs = append(dirs, unitDir{dir: u.systemUnits})
	}
	return dirs
}

// instances counts the extra workers (maktaba-node@1..N): a node converted by its update
// keeps that many ([pipeline] instances), as `install-node --full --instances N` would set.
func (u workerUnits) instances() int {
	switch u.goos {
	case "windows":
		n := 0
		for i := 1; i <= 16; i++ {
			if u.run("schtasks", "/Query", "/TN", fmt.Sprintf("maktaba-node@%d", i)) == nil {
				n++
			}
		}
		return n
	case "darwin":
		matches, _ := filepath.Glob(filepath.Join(u.home, "Library", "LaunchAgents", "com.maktaba.node.*.plist"))
		return len(matches)
	}
	n := 0
	for _, d := range u.unitDirs() {
		n += len(enabledInstances(d.dir))
	}
	return n
}

// enabledInstances are the worker template's enabled instances in a systemd folder
// (maktaba-node@N.service, linked under a *.target.wants folder).
func enabledInstances(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.target.wants", "maktaba-node@*.service"))
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		if name := filepath.Base(m); !seen[name] && name != "maktaba-node@.service" {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// remove stops and removes every worker unit there is (none: nothing done), and says how
// many it removed.
func (u workerUnits) remove() int {
	removed := 0
	switch u.goos {
	case "windows":
		names := []string{"maktaba-node"}
		for i := 1; i <= 16; i++ {
			names = append(names, fmt.Sprintf("maktaba-node@%d", i))
		}
		for _, n := range names {
			if u.run("schtasks", "/Query", "/TN", n) == nil {
				_ = u.run("schtasks", "/End", "/TN", n)
				if u.run("schtasks", "/Delete", "/F", "/TN", n) == nil {
					u.say("  removed the worker task %s", n)
					removed++
				}
			}
		}
	case "darwin":
		matches, _ := filepath.Glob(filepath.Join(u.home, "Library", "LaunchAgents", "com.maktaba.node*.plist"))
		for _, p := range matches {
			_ = u.run("launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), p)
			if os.Remove(p) == nil {
				u.say("  removed the worker agent %s", filepath.Base(p))
				removed++
			}
		}
	default:
		for _, d := range u.unitDirs() {
			systemctl := func(args ...string) error {
				if d.user {
					args = append([]string{"--user"}, args...)
				}
				return u.run("systemctl", args...)
			}
			// the template's instances first (maktaba-node@N: disabled by their own names,
			// the template's file is no unit to stop), then the units' files
			for _, name := range enabledInstances(d.dir) {
				_ = systemctl("disable", "--now", name)
				u.say("  stopped the worker %s", name)
			}
			matches, _ := filepath.Glob(filepath.Join(d.dir, "maktaba-node*.service"))
			for _, p := range matches {
				name := filepath.Base(p)
				if name != "maktaba-node@.service" {
					_ = systemctl("disable", "--now", name)
				}
				if os.Remove(p) == nil {
					u.say("  removed the worker unit %s", name)
					removed++
				}
			}
			if len(matches) > 0 {
				_ = systemctl("daemon-reload")
			}
		}
	}
	return removed
}

// rootSystemUnits is the system's systemd folder when this runs as root on Linux (a root
// install's worker units), else "".
func rootSystemUnits() string {
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		return "/etc/systemd/system"
	}
	return ""
}

// leftoverWorkerUnits removes, at an installed full node's start, the Epic 31 worker units
// a conversion left (story 35.8 AC8): its health wait was missed, or the machine stopped
// between the new service's registration and their removal. This node runs the pipeline
// itself, so they would run a second one for good. Idempotent: every later start finds
// none.
func leftoverWorkerUnits(ctx context.Context) {
	home, _ := os.UserHomeDir()
	u := workerUnits{goos: runtime.GOOS, home: home, systemUnits: rootSystemUnits(),
		run: func(name string, args ...string) error {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			c := exec.CommandContext(cctx, name, args...)
			hideConsole(c)
			return c.Run()
		},
		say: func(format string, a ...any) {
			fmt.Fprintf(os.Stderr, "[serve] node: "+format+"\n", a...)
		}}
	if n := u.remove(); n > 0 {
		fmt.Fprintf(os.Stderr, "[serve] node: %d worker unit(s) of this machine's worker-only install removed: it runs the pipeline itself now\n", n)
	}
}
