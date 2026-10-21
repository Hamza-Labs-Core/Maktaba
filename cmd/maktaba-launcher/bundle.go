package main

// App-bundle mode (Epic 36, stories 36.7/36.8): inside Maktaba.app the server
// payload lives in Contents/Resources/server, signed with the app and
// read-only. There are no release pointers there: the app updater replaces
// the whole app, so the launcher starts the bundled maktaba-server beside it,
// restarts it on exit 75 and with backoff on a crash, keeps its lock and log
// in a writable state directory, and never updates itself or the server.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// EnvAppBundle is set for the server (and every role it starts) to the
// payload directory (…/Maktaba.app/Contents/Resources/server). It tells the
// server it runs from the app: updates arrive with the app, not on their own.
const EnvAppBundle = "MAKTABA_APP_BUNDLE"

// envUpdateDisable is the API's no-network update kill switch (Epic 28/35):
// inside the app there is one updater, the app's.
const envUpdateDisable = "MAKTABA_UPDATE_DISABLE"

// bundlePayload reports whether binDir is <X>.app/Contents/Resources/server/bin
// and returns the payload directory (its parent).
func bundlePayload(binDir string) (string, bool) {
	binDir = filepath.Clean(binDir)
	server := filepath.Dir(binDir)
	resources := filepath.Dir(server)
	contents := filepath.Dir(resources)
	app := filepath.Dir(contents)
	if filepath.Base(binDir) == "bin" && filepath.Base(server) == "server" &&
		filepath.Base(resources) == "Resources" && filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(app, ".app") {
		return server, true
	}
	return "", false
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

// appStateRoot is where an app-bundle launcher keeps its lock and log:
// --root, $MAKTABA_INSTALL_ROOT, else ~/Library/Application Support/Maktaba.
func appStateRoot(flagRoot string) string {
	if flagRoot != "" {
		return flagRoot
	}
	if r := os.Getenv(layout.EnvInstallRoot); r != "" {
		return r
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Library", "Application Support", "Maktaba")
}
