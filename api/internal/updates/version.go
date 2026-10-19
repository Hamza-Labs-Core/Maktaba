package updates

import (
	"regexp"
	"strconv"
	"strings"
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

// IsPrerelease reports whether v carries a prerelease suffix.
func IsPrerelease(v string) bool { return strings.Contains(NormalizeVersion(v), "-") }

// CompareVersions compares release versions numerically (never lexically): >0 when a is
// newer. A prerelease ranks below its release; prereleases of one core compare by their
// dot-separated identifiers, numeric ones numerically. The same rules as
// releaseverify.CompareVersions (story 35.1), so the catalog and the verifier agree.
func CompareVersions(a, b string) int {
	ac, ap := splitVersion(a)
	bc, bp := splitVersion(b)
	for i := 0; i < len(ac) || i < len(bc); i++ {
		var x, y int
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		if x != y {
			return cmpInt(x, y)
		}
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		xn, xerr := strconv.Atoi(as[i])
		yn, yerr := strconv.Atoi(bs[i])
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				return cmpInt(xn, yn)
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func splitVersion(v string) ([]int, string) {
	v = NormalizeVersion(v)
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	if v == "" {
		return nil, pre
	}
	var out []int
	for _, f := range strings.Split(v, ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, pre
		}
		out = append(out, n)
	}
	return out, pre
}

func cmpInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	}
	return 0
}

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
