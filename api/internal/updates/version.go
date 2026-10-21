package updates

import (
	"regexp"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// versionRe is a release version without the "v": MAJOR.MINOR.PATCH and an optional
// prerelease suffix (releaseverify's tag rule without the leading v).
var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// NormalizeVersion strips a leading "v" and spaces.
func NormalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// ValidVersion reports whether v ("1.5.0", "v1.5.0-rc.1") is a release version.
func ValidVersion(v string) bool { return versionRe.MatchString(NormalizeVersion(v)) }

// IsPrerelease reports whether v is a prerelease, or a `git describe` build of
// one; a build made off a release tag (0.2.0-12-gabc1234) is not
// (releaseverify.IsPrerelease).
func IsPrerelease(v string) bool { return releaseverify.IsPrerelease(v) }

// CompareVersions compares release versions numerically (never lexically): >0 when a is
// newer. It is releaseverify.CompareVersions (story 35.1), so the catalog, the scheduler
// and the verifier agree: a prerelease ranks below its release, prereleases of one core
// compare by their dot-separated identifiers (numeric ones numerically), and a `git
// describe` build ranks after the tag it was built from.
func CompareVersions(a, b string) int { return releaseverify.CompareVersions(a, b) }

// maxVersion is the higher of a and b ("" counts as lowest).
func maxVersion(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" || CompareVersions(a, b) >= 0 {
		return a
	}
	return b
}
