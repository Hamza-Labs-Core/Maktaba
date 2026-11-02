// Package version holds the release version and tag rules every Maktaba updater shares
// (releaseverify re-exports them as CompareVersions, IsPrerelease, SameLine and
// ValidTag). It imports the standard library only, so a tool that only orders releases
// (the release workflows' tag guard, tools/release/releaseorder) builds without
// downloading a module: releaseverify itself pulls in Sigstore, gRPC and protobuf.
package version

import (
	"regexp"
	"strconv"
	"strings"
)

// tagRe: vMAJOR.MINOR.PATCH with an optional prerelease suffix. Anything else (refs with
// slashes, spaces, …) never reaches a signing identity's SAN.
var tagRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// ValidTag reports whether tag is a release tag releaseverify.Verify accepts.
func ValidTag(tag string) bool { return tagRe.MatchString(tag) }

// SameLine reports whether two versions are on one major.minor line (1.5.0 and
// 1.5.2-rc.1: yes; 0.2.5 and 0.3.0: no). An unparseable version is on no line.
func SameLine(a, b string) bool {
	ta, _, _ := splitDescribe(a)
	tb, _, _ := splitDescribe(b)
	ca, _ := splitVersion(ta)
	cb, _ := splitVersion(tb)
	if len(ca) < 2 || len(cb) < 2 {
		return false
	}
	return ca[0] == cb[0] && ca[1] == cb[1]
}

// Compare compares release versions ("1.5.0", "v1.5.0", "1.5.0-rc.1"):
// numeric core first, then a prerelease ranks below its release; prereleases of
// the same core compare by their dot-separated identifiers (numeric ones
// numerically). Unparseable cores compare as empty, below every release. A
// `git describe` build ranks after the tag it was built from (a machine running
// one is not behind that release), by its commits after the tag, then dirty
// after clean: v0.2.0 < v0.2.0-dirty < v0.2.0-1-gabc1234 <
// v0.2.0-1-gabc1234-dirty < v0.2.1.
func Compare(a, b string) int {
	ta, na, da := splitDescribe(a)
	tb, nb, db := splitDescribe(b)
	if c := compareReleases(ta, tb); c != 0 {
		return c
	}
	if c := cmpInt(na, nb); c != 0 {
		return c
	}
	switch {
	case da == db:
		return 0
	case da:
		return 1
	}
	return -1
}

// IsPrerelease reports whether v is a prerelease, or a `git describe` build of
// one (1.0.0-rc.1, 1.0.0-rc.1-3-g1a2b3c4d); a build of a release tag
// (0.2.0-12-gabc1234) is not.
func IsPrerelease(v string) bool {
	tag, _, _ := splitDescribe(v)
	_, pre := splitVersion(tag)
	return pre != ""
}

// describeBuild and splitDescribe are layout.SplitDescribe's (this package
// does not import layout, so the modules that verify releases need not): the
// release tag (normalized: no "v", no build metadata), the commits after it,
// whether the tree was dirty.
var describeBuild = regexp.MustCompile(`^(\d+\.\d+\.\d+(?:-[0-9a-z.]+)?)-(\d+)-g[0-9a-f]{4,40}$`)

func splitDescribe(v string) (tag string, commits int, dirty bool) {
	v = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(v)), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if t, ok := strings.CutSuffix(v, "-dirty"); ok {
		v, dirty = t, true
	}
	if m := describeBuild.FindStringSubmatch(v); m != nil {
		if n, err := strconv.Atoi(m[2]); err == nil {
			return m[1], n, dirty
		}
	}
	return v, 0, dirty
}

func compareReleases(a, b string) int {
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

func cmpInt(x, y int) int {
	switch {
	case x > y:
		return 1
	case x < y:
		return -1
	}
	return 0
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(v)), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	if v == "" {
		return nil, pre
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, pre
		}
		out = append(out, n)
	}
	return out, pre
}
