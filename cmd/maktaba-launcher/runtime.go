package main

// The Linux app's runtime copy (Epic 39 story 39.3,
// specs/epics/39-linux-app/contract-runtime.md §4): the systemd user unit runs
// <data>/runtime/current/bin/maktaba-launcher, a copy of the app's payload the
// user owns. It runs in app-bundle mode like Maktaba.app: the payload's server,
// no release pointers, no self-update.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// runtimeCurrent is the runtime's link to the copy the service runs.
const runtimeCurrent = "current"

// runtimePayload reports whether binDir (symlinks resolved) is the bin/ of a
// runtime copy: <runtime>/<dir>/bin beside a payload.json for this machine.
// The payload it returns is <runtime>/current when that link points at this
// copy, so the paths every role gets (the bundled Python the venv is built
// with) stay the same across updates.
func runtimePayload(binDir string) (string, bool) {
	binDir = filepath.Clean(binDir)
	payload := filepath.Dir(binDir)
	if filepath.Base(binDir) != "bin" || !payloadFor(payload, payloadPlatform()) {
		return "", false
	}
	current := filepath.Join(filepath.Dir(payload), runtimeCurrent)
	if resolved, err := filepath.EvalSymlinks(current); err == nil && sameDir(resolved, payload) {
		return current, true
	}
	return payload, true
}

// payloadFor reports a payload.json (schema 1) for platform in dir.
func payloadFor(dir, platform string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "payload.json"))
	if err != nil {
		return false
	}
	var p struct {
		Schema   int    `json:"schema"`
		Platform string `json:"platform"`
	}
	return json.Unmarshal(b, &p) == nil && p.Schema == 1 && p.Platform == platform
}

// payloadPlatform is this machine's payload platform (payload.json's "platform").
func payloadPlatform() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return runtime.GOOS + "-" + arch
}

func sameDir(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// runtimeStateRoot is where a runtime launcher keeps its lock and log: --root,
// $MAKTABA_INSTALL_ROOT, else the runtime's parent (the platform's data
// directory, $XDG_DATA_HOME/maktaba on Linux).
func runtimeStateRoot(flagRoot, payload string) string {
	if flagRoot != "" {
		return flagRoot
	}
	if r := os.Getenv("MAKTABA_INSTALL_ROOT"); r != "" {
		return r
	}
	return filepath.Dir(filepath.Dir(payload))
}
