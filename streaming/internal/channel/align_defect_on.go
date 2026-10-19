//go:build tvdefect

package channel

// A tvdefect build (the staging verification e2e test's broken releases)
// applies BuildDefect.
func init() { applyBuildDefect(BuildDefect) }
