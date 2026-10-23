package main

// App-bundle mode: the server payload ships inside the desktop app, read-only
// and replaced whole by the app's updater.
//
//	macOS   (Epic 36, stories 36.7/36.8): Maktaba.app/Contents/Resources/server,
//	        signed with the app
//	Windows (Epic 38, story 38.2): <install>\server beside <install>\Maktaba.exe
//	        (the NSIS install, normally %LOCALAPPDATA%\Maktaba)
//
// There are no release pointers there: the app updater replaces the whole
// app, so the launcher starts the bundled maktaba-server beside it, restarts
// it on exit 75 and with backoff on a crash, keeps its lock and log in a
// writable state directory, and never updates itself or the server. On
// Windows the app's agent stops it through a named event (stopevent_windows.go).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// EnvAppBundle is set for the server (and every role it starts) to the
// payload directory (…/Maktaba.app/Contents/Resources/server, or
// <install>\server on Windows). It tells the server it runs from the app:
// updates arrive with the app, not on their own.
const EnvAppBundle = "MAKTABA_APP_BUNDLE"

// EnvAppBundled is the app's own payload, set beside MAKTABA_APP_BUNDLE when a kept
// copy of an earlier payload runs in its place (a rolled-back app update, Epic 35).
const EnvAppBundled = "MAKTABA_APP_BUNDLED"

// EnvLauncherRoot is the launcher's state root: where the server's update leaves this
// launcher's guard (updates/pending.json) and the payload override (Epic 35).
const EnvLauncherRoot = "MAKTABA_LAUNCHER_ROOT"

// The Windows app's files that mark <install> (contract-agent of Epic 38):
// the shell beside the payload, and the payload's manifest.
const (
	winAppExe   = "Maktaba.exe"
	payloadJSON = "payload.json"
)

// bundlePayload reports whether binDir is the bin directory of an app payload
// and returns the payload directory (its parent):
//
//	macOS   : <X>.app/Contents/Resources/server/bin
//	Windows : <install>/server/bin, with <install>/server/payload.json and
//	          <install>/Maktaba.exe
//
// The Mac rule is the path alone (a bundle's shape is fixed by macOS). The
// Windows install is a plain folder the user may have chosen, so its rule
// also asks for the files only the app puts there. Both are checked on every
// OS: they depend on the files, not on where the tests run.
func bundlePayload(binDir string) (string, bool) {
	binDir = filepath.Clean(binDir)
	server := filepath.Dir(binDir)
	if filepath.Base(binDir) != "bin" || filepath.Base(server) != "server" {
		return "", false
	}
	up := filepath.Dir(server) // macOS: <X>.app/Contents/Resources; Windows: <install>
	contents := filepath.Dir(up)
	if filepath.Base(up) == "Resources" && filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(filepath.Dir(contents), ".app") {
		return server, true
	}
	if isFile(filepath.Join(server, payloadJSON)) && isFile(filepath.Join(up, winAppExe)) {
		return server, true
	}
	return "", false
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// selfDir is the directory of the running executable, symlinks resolved.
func selfDir() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	return filepath.Dir(self)
}

// appStateRoot is where an app-bundle launcher keeps its lock and log (and
// where the server runs): --root, $MAKTABA_INSTALL_ROOT, else the app's state
// folder (appStateRootFor).
func appStateRoot(flagRoot string) string {
	home, _ := os.UserHomeDir()
	return appStateRootFor(flagRoot, os.Getenv, runtime.GOOS, home)
}

// appStateRootFor resolves appStateRoot for an OS (tests pass their own
// environment and home):
//
//	Windows : %LOCALAPPDATA%\Maktaba (<home>\AppData\Local\Maktaba when unset),
//	          the folder the NSIS install and its state share
//	else    : ~/Library/Application Support/Maktaba (the Mac app)
func appStateRootFor(flagRoot string, getenv func(string) string, goos, home string) string {
	if flagRoot != "" {
		return flagRoot
	}
	if r := getenv(layout.EnvInstallRoot); r != "" {
		return r
	}
	if goos == "windows" {
		if local := getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Maktaba")
		}
		if home == "" {
			return "."
		}
		return filepath.Join(home, "AppData", "Local", "Maktaba")
	}
	if home == "" {
		return "."
	}
	return filepath.Join(home, "Library", "Application Support", "Maktaba")
}
