package router

// Epic 57 review, finding 20: the bypass rehearsal (a job of
// .github/workflows/_integration.yml) requires every test it names to have run and
// passed. The names are in tools/ci/parental-bypass-tests.txt; this test holds that
// list to the code, so a test that is renamed or added changes the list with it.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(append([]string{filepath.Dir(file), "..", "..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestParentalBypassListNamesEveryTest(t *testing.T) {
	workflow := repoFile(t, ".github", "workflows", "_integration.yml")
	m := regexp.MustCompile(`go test -tags parental_bypass[^\n]*\\\n\s+-run '([^']+)'`).FindStringSubmatch(workflow)
	if m == nil {
		t.Fatal("the rehearsal's go test line is not in _integration.yml as this test knows it")
	}
	run := regexp.MustCompile(m[1])
	if !strings.Contains(workflow, "done < ../tools/ci/parental-bypass-tests.txt") {
		t.Fatal("the rehearsal no longer requires the tests of tools/ci/parental-bypass-tests.txt")
	}

	// every test of this package the rehearsal's -run names, with or without the tag
	_, file, _, _ := runtime.Caller(0)
	sources, err := filepath.Glob(filepath.Join(filepath.Dir(file), "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)
	var named []string
	for _, src := range sources {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fn.FindAllStringSubmatch(string(raw), -1) {
			if run.MatchString(f[1]) {
				named = append(named, f[1])
			}
		}
	}
	sort.Strings(named)

	var listed []string
	for _, line := range strings.Split(repoFile(t, "tools", "ci", "parental-bypass-tests.txt"), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			listed = append(listed, line)
		}
	}
	if !sort.StringsAreSorted(listed) {
		t.Error("tools/ci/parental-bypass-tests.txt is not sorted")
	}
	if strings.Join(listed, "\n") != strings.Join(named, "\n") {
		t.Fatalf("tools/ci/parental-bypass-tests.txt and the tests the rehearsal runs differ.\nlisted:\n%s\n\nin the code:\n%s",
			strings.Join(listed, "\n"), strings.Join(named, "\n"))
	}
	for _, must := range []string{"TestParentalBypass", "TestParentalBypassCatchesAMissingFilter", "TestP57RoleTable",
		"TestPGTimeLimitRefusesStreams", "TestDeviceSignalRidesTheAccessToken", "TestEveryRouteHasParentalSurface"} {
		if i := sort.SearchStrings(listed, must); i == len(listed) || listed[i] != must {
			t.Errorf("%s is not required by the rehearsal", must)
		}
	}
}
