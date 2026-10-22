package appruntime

import "testing"

// A `git describe` build comes after the tag it was built from, never before
// it as a pre-release would.
func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v0.2.0-12-gabc1234", "v0.2.0", 1},
		{"v0.2.0", "v0.2.0-12-gabc1234", -1},
		{"v0.2.0-12-gabc1234", "v0.2.0-3-g9f8e7d6", 1},
		{"v0.2.0-12-gabc1234", "v0.2.0-12-gdeadbee", 0},
		// a dirty tree after the clean build of the same commit, before the next commit
		{"v0.2.0-12-gabc1234-dirty", "v0.2.0-12-gdeadbee", 1},
		{"v0.2.0-dirty", "v0.2.0", 1},
		{"v0.2.0-dirty", "v0.2.0-1-gabc1234", -1},
		{"v0.2.0-12-gabc1234", "v0.2.1", -1},
		{"v0.3.0-rc.1", "v0.2.0-40-gabc1234", 1},
		{"v1.0.0-rc.1-3-g1a2b3c4d", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.1-3-g1a2b3c4d", "v1.0.0", -1},
		{"v1.0.0-rc.1", "v1.0.0", -1}, // a pre-release still comes first
		{"1.2.3", "v1.2.3", 0},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
