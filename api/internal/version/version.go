// Package version exposes build-time metadata injected via -ldflags.
//
// All three variables are stamped at link time by tools/build.sh and the
// Makefile's build target. They default to "unknown" so a developer who
// runs `go build` directly (without ldflags) still gets a runnable binary
// — they just can't introspect the version.
//
// Story 22.2 owns the injection contract; consumers (`/healthz`,
// `--version`, log fields) read these as plain strings.
package version

import (
	"os"
	"strings"
)

var (
	// Version is the release tag (`git describe --tags --dirty --always`).
	Version = "unknown"

	// Commit is the full git SHA.
	Commit = "unknown"

	// BuildDate is the unix timestamp of the source commit (SOURCE_DATE_EPOCH),
	// not wall-clock time. Two builds of the same commit get the same value.
	BuildDate = "unknown"
)

// String returns a single-line human-readable version stamp.
func String() string {
	return Version + " (" + Commit + ", built " + BuildDate + ")"
}

// Channel reports the update channel this build belongs to (Story 28.1).
//
// Precedence: an explicit MAKTABA_UPDATE_CHANNEL operator override wins
// (so an operator can track beta on a stable build, or pin stable); else
// it is derived from the version string — a prerelease suffix
// (-beta / -rc / -alpha) or a "nightly" marker means "beta", everything
// else (including the "unknown"/"dev" dev default) means "stable".
//
// The update-check service (Story 28.2) uses this to decide whether to
// consider GitHub prereleases.
func Channel() string {
	if c := strings.TrimSpace(os.Getenv("MAKTABA_UPDATE_CHANNEL")); c != "" {
		return strings.ToLower(c)
	}
	v := strings.ToLower(Version)
	switch {
	case strings.Contains(v, "-beta"),
		strings.Contains(v, "-rc"),
		strings.Contains(v, "-alpha"),
		strings.Contains(v, "nightly"):
		return "beta"
	default:
		return "stable"
	}
}
