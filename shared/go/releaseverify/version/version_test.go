package version_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/version"
)

// The package's point: the standard library only. The release workflows' tag guard builds
// tools/release/cmd/release-order on it offline (tools/release/tag-guard-test.sh runs with
// GOPROXY=off); an import of releaseverify or any module would make every guard download
// Sigstore's dependencies again (CI run 41476114340 failed on one of those downloads).
func TestImportsTheStandardLibraryOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		if p != "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/version" {
			t.Errorf("imports %s: the version package must stay standard library only", p)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.5.0", "1.4.9", 1},
		{"v1.5.0", "1.5.0", 0},
		{"1.5.0-rc.2", "1.5.0-rc.10", -1},
		{"1.5.0-rc.1", "1.5.0", -1},
		{"v0.2.0-12-gabc1234", "v0.2.0", 1},
		{"v0.2.0-dirty", "v0.2.0-1-gabc1234", -1},
	} {
		if got := version.Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestTagsLinesPrereleases(t *testing.T) {
	for tag, want := range map[string]bool{
		"v0.3.0": true, "v0.3.0-rc.7": true, "0.3.0": false, "v0.3": false, "refs/tags/v0.3.0": false, "v0.3.0 x": false,
	} {
		if got := version.ValidTag(tag); got != want {
			t.Errorf("ValidTag(%q) = %v, want %v", tag, got, want)
		}
	}
	if !version.SameLine("1.5.0", "1.5.2-rc.1") || version.SameLine("0.2.5", "0.3.0") || version.SameLine("x", "1.5.0") {
		t.Error("SameLine")
	}
	if !version.IsPrerelease("v1.0.0-rc.1-3-g1a2b3c4d") || version.IsPrerelease("0.2.0-12-gabc1234") {
		t.Error("IsPrerelease")
	}
}
