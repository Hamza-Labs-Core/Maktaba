package router

// Epic 58 story 58.17: the tv-clients rehearsal (a job of .github/workflows/_integration.yml)
// requires every test it names to have run and passed. The names are in
// tools/ci/tv-clients-tests.txt; this test holds that list to the code (this package's tests,
// and streaming/internal/capability's for the job's second step), so a test that is renamed or
// added changes the list with it.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// testsNamed are the Test functions of dir's _test.go files that run matches.
func testsNamed(t *testing.T, dir string, run *regexp.Regexp) []string {
	t.Helper()
	sources, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
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
	return named
}

func TestTVClientsListNamesEveryTest(t *testing.T) {
	workflow := repoFile(t, ".github", "workflows", "_integration.yml")
	job, ok := strings.CutPrefix(workflow[strings.Index(workflow, "\n  tv-clients:"):], "\n")
	if !ok || !strings.Contains(job, "if: inputs.tv_clients") {
		t.Fatal("the tv-clients job is not in _integration.yml as this test knows it")
	}
	api := regexp.MustCompile(`go test -count=1 -timeout 10m -v \\\n\s+-run '([^']+)'\s*\\\n\s+\./internal/router/`).FindStringSubmatch(job)
	streaming := regexp.MustCompile(`go test -count=1 -v -run '([^']+)' \./internal/capability/`).FindStringSubmatch(job)
	if api == nil || streaming == nil {
		t.Fatal("the rehearsal's go test lines are not in the tv-clients job as this test knows them")
	}
	if strings.Count(job, "done < ../tools/ci/tv-clients-tests.txt") != 2 {
		t.Fatal("the rehearsal's two steps no longer require the tests of tools/ci/tv-clients-tests.txt")
	}

	_, file, _, _ := runtime.Caller(0)
	here := filepath.Dir(file)
	named := testsNamed(t, here, regexp.MustCompile(api[1]))
	for _, n := range testsNamed(t, filepath.Join(here, "..", "..", "..", "streaming", "internal", "capability"), regexp.MustCompile(streaming[1])) {
		named = append(named, "streaming:"+n)
	}

	var listed []string
	for _, line := range strings.Split(repoFile(t, "tools", "ci", "tv-clients-tests.txt"), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			listed = append(listed, line)
		}
	}
	if strings.Join(listed, "\n") != strings.Join(named, "\n") {
		t.Fatalf("tools/ci/tv-clients-tests.txt and the tests the rehearsal runs differ.\nlisted:\n%s\n\nin the code:\n%s",
			strings.Join(listed, "\n"), strings.Join(named, "\n"))
	}
	// the story's steps: compat, the cloud code, sessions, cast grants, parental, the tables
	for _, must := range []string{"TestTVClientsCompat", "TestTVClientsCloudCode", "TestTVClientsSessions",
		"TestPGCastGrantVideo", "TestP58BypassCastGrantAfterTighten", "TestP58BypassCloudCodeApprover",
		"TestP58RoleTable", "TestP58RoleTableNamesEveryRoute", "TestCastGrantWalkRefusesEveryOtherRoute",
		"TestClientFloorAtSignIn", "streaming:TestCapsVectors"} {
		found := false
		for _, l := range listed {
			found = found || l == must
		}
		if !found {
			t.Errorf("%s is not required by the rehearsal", must)
		}
	}
}
