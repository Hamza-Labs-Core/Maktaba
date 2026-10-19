package layout

import (
	"sort"
	"strconv"
	"strings"
)

// CompareVersions compares two release versions numerically (1.10.0 >
// 1.9.0): major.minor.patch, then a release ranks above its prereleases
// (1.5.0 > 1.5.0-rc.2 > 1.5.0-rc.1). Build metadata (+...) is ignored. A
// leading "v" is accepted.
func CompareVersions(a, b string) int {
	a, b = NormalizeVersion(a), NormalizeVersion(b)
	if i := strings.IndexByte(a, '+'); i >= 0 {
		a = a[:i]
	}
	if i := strings.IndexByte(b, '+'); i >= 0 {
		b = b[:i]
	}
	ac, ap, _ := strings.Cut(a, "-")
	bc, bp, _ := strings.Cut(b, "-")
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
