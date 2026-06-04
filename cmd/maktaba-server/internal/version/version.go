// Package version carries the build-stamped version metadata for the
// unified maktaba-server binary. The three vars are overridden at link
// time via -ldflags '-X .../version.Version=...' exactly like the api
// and streaming modules' version packages, so `make server` and the
// release pipeline stamp the same VERSION/COMMIT/BUILD_DATE triple.
package version

var (
	// Version is the semver-ish release string (git describe output).
	Version = "dev"
	// Commit is the short git SHA the binary was built from.
	Commit = "unknown"
	// BuildDate is the SOURCE_DATE_EPOCH the binary was built at.
	BuildDate = "unknown"
)

// String renders the human-facing one-liner printed by `--version`.
func String() string {
	return "maktaba-server " + Version + " (" + Commit + ", built " + BuildDate + ")"
}
