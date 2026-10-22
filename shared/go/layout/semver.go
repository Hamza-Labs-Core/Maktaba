package layout

import (
	"cmp"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// describeBuild is a `git describe` build once normalized (no "v", no build
// metadata, no -dirty): a tag, the commits after it and the commit
// (0.2.0-12-gabc1234, 1.0.0-rc.1-3-g1a2b3c4d), the version of a build made off
// a tag (payload.sh, `make VERSION=...` from a checkout).
var describeBuild = regexp.MustCompile(`^(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)-(\d+)-g[0-9a-f]{4,40}$`)

// SplitDescribe is a version's release tag (normalized: no "v", no build
// metadata), the commits after it and whether its tree was dirty: a `git
// describe` build (v0.2.0-12-gabc1234, -dirty too, and v0.2.0-dirty on the tag
// itself) is that many commits after its tag; any other version is itself, 0,
// clean. releaseverify keeps the same rule (its splitDescribe).
func SplitDescribe(v string) (tag string, commits int, dirty bool) {
	v = NormalizeVersion(v)
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

// CompareVersions compares two release versions numerically (1.10.0 >
// 1.9.0): major.minor.patch, then a release ranks above its prereleases
// (1.5.0 > 1.5.0-rc.2 > 1.5.0-rc.1). Build metadata (+...) is ignored. A
// leading "v" is accepted. A `git describe` build ranks after the tag it was
// built from, never before it as a prerelease would: running one is never
// "older than" that release. By its commits after the tag, then dirty after
// clean: v0.2.0 < v0.2.0-dirty < v0.2.0-1-gabc1234 < v0.2.0-1-gabc1234-dirty <
// v0.2.1.
func CompareVersions(a, b string) int {
	ta, na, da := SplitDescribe(a)
	tb, nb, db := SplitDescribe(b)
	if c := compareReleases(ta, tb); c != 0 {
		return c
	}
	if c := cmp.Compare(na, nb); c != 0 {
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

func compareReleases(a, b string) int {
	a, b = strings.ToLower(NormalizeVersion(a)), strings.ToLower(NormalizeVersion(b))
	if i := strings.IndexByte(a, '+'); i >= 0 {
		a = a[:i]
	}
	if i := strings.IndexByte(b, '+'); i >= 0 {
		b = b[:i]
	}
	ac, ap, _ := strings.Cut(a, "-")
	bc, bp, _ := strings.Cut(b, "-")
	// a version whose core is not numbers ("dev", a branch's name) is no release:
	// below every one, as the release verifier orders it
	switch an, bn := numericCore(ac), numericCore(bc); {
	case an && !bn:
		return 1
	case !an && bn:
		return -1
	case !an && !bn:
		return strings.Compare(a, b)
	}
	if c := compareDotted(ac, bc, true); c != 0 {
		return c
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return compareDotted(ap, bp, false)
}

// numericCore: c is dot-separated numbers ("1.5.0", "1.5").
func numericCore(c string) bool {
	for _, p := range strings.Split(c, ".") {
		if _, err := strconv.ParseUint(p, 10, 64); err != nil {
			return false
		}
	}
	return true
}

func compareDotted(a, b string, core bool) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var x, y string
		if i < len(as) {
			x = as[i]
		} else if core {
			x = "0"
		}
		if i < len(bs) {
			y = bs[i]
		} else if core {
			y = "0"
		}
		if x == y {
			continue
		}
		if x == "" {
			return -1
		}
		if y == "" {
			return 1
		}
		xi, xerr := strconv.ParseUint(x, 10, 64)
		yi, yerr := strconv.ParseUint(y, 10, 64)
		switch {
		case xerr == nil && yerr == nil:
			if xi < yi {
				return -1
			}
			return 1
		case xerr == nil: // numeric identifiers rank below alphanumeric ones
			return -1
		case yerr == nil:
			return 1
		}
		if x < y {
			return -1
		}
		return 1
	}
	return 0
}

// SortVersions sorts oldest first.
func SortVersions(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool { return CompareVersions(vs[i], vs[j]) < 0 })
}
