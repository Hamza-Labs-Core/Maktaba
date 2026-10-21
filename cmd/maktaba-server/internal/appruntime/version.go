package appruntime

import (
	"regexp"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// describeBuild is a `git describe` build: a tag, the commits after it and the
// commit (v0.2.0-12-gabc1234, v1.0.0-rc.1-3-g1a2b3c4d-dirty). payload.sh
// versions a payload built off a tag this way.
var describeBuild = regexp.MustCompile(`^(v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)-(\d+)-g[0-9a-f]{4,40}(?:-dirty)?$`)

// CompareVersions orders payload versions (-1, 0, 1): releases as
// layout.CompareVersions does (a pre-release before its release), and a
// `git describe` build after the tag it was built from (by its number of
// commits after it), never before it as a pre-release would be.
func CompareVersions(a, b string) int {
	ta, na := splitDescribe(a)
	tb, nb := splitDescribe(b)
	if c := layout.CompareVersions(ta, tb); c != 0 {
		return c
	}
	switch {
	case na < nb:
		return -1
	case na > nb:
		return 1
	}
	return 0
}

// splitDescribe is a version's tag and its commits after the tag (0: the tag).
func splitDescribe(v string) (string, int) {
	m := describeBuild.FindStringSubmatch(v)
	if m == nil {
		return v, 0
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return v, 0
	}
	return m[1], n
}
