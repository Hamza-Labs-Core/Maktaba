package releaseverify

import "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/version"

// The version rules live in the dependency-free package version (a tool that only orders
// releases imports it without Sigstore); these are its names here.

// CompareVersions is version.Compare: release versions ("1.5.0", "v1.5.0", "1.5.0-rc.1",
// a `git describe` build after its tag), -1, 0 or 1.
func CompareVersions(a, b string) int { return version.Compare(a, b) }

// IsPrerelease is version.IsPrerelease: a prerelease, or a `git describe` build of one.
func IsPrerelease(v string) bool { return version.IsPrerelease(v) }

// SameLine is version.SameLine: two versions on one major.minor line.
func SameLine(a, b string) bool { return version.SameLine(a, b) }

// ValidTag is version.ValidTag: a release tag Verify accepts (vX.Y.Z or vX.Y.Z-pre).
func ValidTag(tag string) bool { return version.ValidTag(tag) }
