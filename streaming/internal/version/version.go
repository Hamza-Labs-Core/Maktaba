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
