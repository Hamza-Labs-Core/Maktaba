// Package selfmanaged converts an install into the side-by-side layout the
// launcher runs (Epic 35, story 35.2 AC8/AC9, design §2.4): the running (or
// packaged) binaries are copied into <install>/bin/releases/<version>/ with a
// .verified record, the pointers are written, and the launcher is put at
// <install>/maktaba-launcher. Configuration (server.toml, secrets.key,
// node.json) is not moved.
package selfmanaged

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Identities recorded in .verified.
const (
	IdentityMigrated = layout.IdentityMigrated // copied from a running pre-layout install
	IdentityPackage  = "package"               // installed by a .deb/.rpm postinstall
)

// Binaries copied into a release directory (the launcher also goes to the root).
var Binaries = []string{"maktaba-server", "maktaba-api", "maktaba-streaming", "maktaba-launcher"}

// Options for Convert.
type Options struct {
	Root     string // install root
	From     string // directory holding the binaries
	Version  string // their version (no "dev")
	Identity string // IdentityMigrated (default) or IdentityPackage
	Keep     int    // keep_versions for the prune (default 3)
	Now      time.Time
	Log      func(string)
}

// Result of Convert.
type Result struct {
	Version  string
	Staged   bool   // the release directory was created
	Switched bool   // current now names Version
	Pending  bool   // switched under the boot-loop guard (an upgrade)
	Launcher bool   // the root's launcher was installed or replaced
	Skipped  string // why current was not changed
	Pruned   []string
}

// Errors.
var (
	ErrDevBuild   = errors.New("a dev build has no release version (pass --version)")
	ErrNoServer   = errors.New("maktaba-server not found")
	ErrNoLauncher = errors.New("maktaba-launcher not found next to the binaries")
)

// Convert stages From as release Version under Root and points current at it
// when it is the first release or newer than both current and the watermark.
// A package older than (or equal to) the watermark only refreshes the
// launcher: the updater owns releases, the package manager the launcher.
func Convert(o Options) (Result, error) {
	if o.Identity == "" {
		o.Identity = IdentityMigrated
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	v := layout.NormalizeVersion(o.Version)
	if v == "" || v == "dev" || strings.HasPrefix(v, "dev") {
		return Result{}, ErrDevBuild
	}
	if !layout.ValidVersion(v) {
		return Result{}, fmt.Errorf("%w: %q", layout.ErrBadVersion, o.Version)
	}
	l := layout.New(o.Root)
	res := Result{Version: v}
	if err := os.MkdirAll(l.ReleasesDir(), 0o755); err != nil {
		return res, err
	}
	if _, err := os.Stat(filepath.Join(o.From, layout.Exe("maktaba-server"))); err != nil {
		return res, fmt.Errorf("%w in %s", ErrNoServer, o.From)
	}

	// 1. the release directory (built aside, renamed into place)
	if err := l.ValidateRelease(v); err != nil {
		if err := stage(l, o, v); err != nil {
			return res, err
		}
		res.Staged = true
		o.Log("staged " + l.ReleaseDir(v))
	}

	// 2. the launcher at the root
	changed, err := installLauncher(l, o.From)
	if err != nil {
		return res, err
	}
	res.Launcher = changed
	if changed {
		o.Log("installed " + l.LauncherPath())
	}

	// 3. pointers
	if _, err := l.Reconcile(); err != nil {
		return res, err
	}
	cur, cerr := l.Current()
	wm := l.Watermark()
	switch {
	case cerr != nil: // first release: it is what runs today, no guard needed
		if err := l.WritePointer(layout.CurrentFile, v); err != nil {
			return res, err
		}
		if err := l.RaiseWatermark(v); err != nil {
			return res, err
		}
		res.Switched = true
	case cur == v:
		res.Skipped = "already current"
	case wm != "" && layout.CompareVersions(v, wm) <= 0:
		res.Skipped = "not newer than the watermark " + wm
	case layout.CompareVersions(v, cur) <= 0:
		res.Skipped = "not newer than current " + cur
	default: // an upgrade: under the boot-loop guard until the server confirms it
		if err := l.Switch(v, "setup", o.Now); err != nil {
			return res, err
		}
		res.Switched, res.Pending = true, true
	}
	if res.Skipped != "" {
		o.Log("current unchanged: " + res.Skipped)
	} else {
		o.Log("current = " + v)
	}

	// 4. prune
	keep := o.Keep
	if keep <= 0 {
		keep = 3
	}
	pr, err := l.Prune(keep)
	if err == nil {
		res.Pruned = pr.Removed
	}
	return res, nil
}

func stage(l layout.Layout, o Options, v string) error {
	tmp := filepath.Join(l.ReleasesDir(), ".staging-"+v+"-"+randHex())
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil { // the launcher refuses a shared-writable release
		return err
	}
	defer os.RemoveAll(tmp)
	for _, name := range Binaries {
		src := filepath.Join(o.From, layout.Exe(name))
		if _, err := os.Stat(src); err != nil {
			if name == "maktaba-server" {
				return fmt.Errorf("%w in %s", ErrNoServer, o.From)
			}
			o.Log("note: " + layout.Exe(name) + " not in " + o.From)
			continue
		}
		if err := layout.CopyFile(src, filepath.Join(tmp, layout.Exe(name))); err != nil {
			return err
		}
	}
	if _, err := layout.WriteVerified(tmp, o.Identity, v, o.Now); err != nil {
		return err
	}
	final := l.ReleaseDir(v)
	if _, err := os.Stat(final); err == nil { // present but not valid: move it aside
		aside := filepath.Join(l.ReleasesDir(), ".broken-"+v+"-"+randHex())
		if err := os.Rename(final, aside); err != nil {
			return fmt.Errorf("move the invalid %s aside: %w", final, err)
		}
		_ = os.RemoveAll(aside)
	}
	return os.Rename(tmp, final)
}

// installLauncher copies From/maktaba-launcher to the root when it differs.
// The running launcher (Windows: a locked .exe) is renamed to .old first; the
// next launcher start deletes it.
func installLauncher(l layout.Layout, from string) (bool, error) {
	src := filepath.Join(from, layout.Exe("maktaba-launcher"))
	dst := l.LauncherPath()
	if _, err := os.Stat(src); err != nil {
		if _, err := os.Stat(dst); err == nil {
			return false, nil // keep the installed one
		}
		return false, fmt.Errorf("%w (%s)", ErrNoLauncher, src)
	}
	want, err := layout.SHA256File(src)
	if err != nil {
		return false, err
	}
	if got, err := layout.SHA256File(dst); err == nil && got == want {
		return false, nil
	}
	if _, err := os.Stat(dst); err == nil {
		_ = os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil {
			return false, fmt.Errorf("move the installed launcher aside: %w", err)
		}
	}
	if err := layout.CopyFile(src, dst); err != nil {
		return false, err
	}
	return true, nil
}

// HomebrewManaged reports whether a binary lives in a Homebrew Cellar (brew
// owns those files: not converted automatically, story 35.2 AC10).
func HomebrewManaged(exe string) bool {
	p := filepath.ToSlash(exe)
	return strings.Contains(p, "/Cellar/") || strings.HasPrefix(p, "/opt/homebrew/")
}

func randHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
