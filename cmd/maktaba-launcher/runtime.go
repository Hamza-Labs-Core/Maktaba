package main

// The Linux app's runtime copy (Epic 39 story 39.3,
// specs/epics/39-linux-app/contract-runtime.md §4): the systemd user unit runs
// <data>/runtime/current/bin/maktaba-launcher, a copy of the app's payload the
// user owns. It runs in app-bundle mode like Maktaba.app: the payload's server,
// no release pointers, and the launcher never updates itself. The runtime's
// owner.json says who updates it: the app (MAKTABA_UPDATE_DISABLE, one updater),
// or the machine itself (owner headless: the server's update installs a new
// runtime, story 39.9).

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
// $MAKTABA_INSTALL_ROOT, else the platform's data directory, as the server's
// config has it: $XDG_DATA_HOME/maktaba, ~/.local/share/maktaba without one.
func runtimeStateRoot(flagRoot string) string {
	if flagRoot != "" {
		return flagRoot
	}
	if r := os.Getenv("MAKTABA_INSTALL_ROOT"); r != "" {
		return r
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "maktaba")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "maktaba")
}

// Owners of a runtime (owner.json, written by `maktaba-server runtime install --owner`).
const (
	runtimeOwnerFile     = "owner.json"
	runtimeOwnerHeadless = "headless"
	// envRuntimeOwner tells the server (and the API) who updates the runtime.
	envRuntimeOwner = "MAKTABA_RUNTIME_OWNER"
)

// runtimeOwner is the owner recorded in a runtime root: "headless", else "app"
// (none recorded, or one this launcher does not know).
func runtimeOwner(root string) string {
	var o struct {
		Owner string `json:"owner"`
	}
	if b, err := os.ReadFile(filepath.Join(root, runtimeOwnerFile)); err == nil &&
		json.Unmarshal(b, &o) == nil && o.Owner == runtimeOwnerHeadless {
		return runtimeOwnerHeadless
	}
	return "app"
}

// updateEnv is the server's update environment in app-bundle mode, read at
// every start so a change of owner applies at the next one. runtimeRoot is
// the Linux runtime the payload is a copy in ("" for Maktaba.app). The server
// updates itself in every case now (Epic 35, install-kinds.md): the home's update
// orders the app to install (Mac, Windows) or installs a new runtime (Linux), so
// MAKTABA_UPDATE_DISABLE is no longer set here.
func updateEnv(runtimeRoot string) []string {
	if runtimeRoot == "" {
		return nil
	}
	return []string{envRuntimeOwner + "=" + runtimeOwner(runtimeRoot)}
}
