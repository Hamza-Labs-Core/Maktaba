package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes a scratch repository with a main branch.
func gitRepo(t *testing.T) (dir string, do func(args ...string) string, write func(rel, content string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir = t.TempDir()
	do = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	write = func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	do("init", "-q", "-b", "main")
	write("api/go.mod", "module example.com/api\n")
	write("api/internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	write("api/internal/a/a_test.go", "package a\n")
	write("docs/x.md", "# x\n")
	do("add", ".")
	do("commit", "-q", "-m", "base")
	do("checkout", "-q", "-b", "feature")
	return dir, do, write
}

func gate(t *testing.T, dir string, extra ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"gate", "-C", dir, "--range", "main...HEAD", "--map", "none"}, extra...), &out, &errb)
	return code, out.String() + errb.String()
}

func TestGateEndToEnd(t *testing.T) {
	dir, do, write := gitRepo(t)

	// docs only: passes
	write("docs/x.md", "# y\n")
	do("commit", "-qam", "docs")
	if code, out := gate(t, dir); code != 0 {
		t.Fatalf("docs-only change blocked:\n%s", out)
	}

	// code without its test: blocked
	write("api/internal/a/a.go", "package a\n\nfunc A() int { return 2 }\n")
	do("commit", "-qam", "change A")
	code, out := gate(t, dir)
	if code != 1 || !strings.Contains(out, "MISSING  api/internal/a/a.go") || !strings.Contains(out, "a_test.go") {
		t.Fatalf("code without test: exit %d\n%s", code, out)
	}
}

func TestGateEndToEndTrailer(t *testing.T) {
	dir, do, write := gitRepo(t)
	write("api/internal/a/a.go", "package a\n\nfunc A() int { return 2 }\n")
	do("commit", "-qam", "change A")
	// a later commit with the trailer: passes, and says why
	do("commit", "-q", "--allow-empty", "-m", "note\n\nNo-Test-Change: constant tweak covered by the existing test")
	code, out := gate(t, dir)
	if code != 0 || !strings.Contains(out, "constant tweak covered by the existing test") {
		t.Fatalf("trailer: exit %d\n%s", code, out)
	}
}

func TestGateEndToEndTestUpdated(t *testing.T) {
	dir, do, write := gitRepo(t)
	write("api/internal/a/a.go", "package a\n\nfunc A() int { return 2 }\n")
	write("api/internal/a/a_test.go", "package a\n\n// checks A\n")
	do("commit", "-qam", "test A")
	if code, out := gate(t, dir); code != 0 {
		t.Fatalf("code with its test: exit %d\n%s", code, out)
	}

	// list as JSON
	var js bytes.Buffer
	if c := run([]string{"list", "-C", dir, "--range", "main...HEAD", "--map", "none", "--json"}, &js, &js); c != 0 ||
		!strings.Contains(js.String(), `"api/internal/a/a_test.go"`) {
		t.Fatalf("json: exit %d\n%s", c, js.String())
	}
}

func TestCheckNoRun(t *testing.T) {
	dir, do, write := gitRepo(t)
	write("api/internal/a/a.go", "package a\n\nfunc A() int { return 3 }\n")
	write("api/internal/a/a_test.go", "package a\n\n// more\n")
	do("commit", "-qam", "both")
	var out bytes.Buffer
	if code := run([]string{"check", "-C", dir, "--range", "main...HEAD", "--map", "none", "--no-run"}, &out, &out); code != 0 {
		t.Fatalf("check --no-run: %d\n%s", code, out.String())
	}
	if strings.Contains(out.String(), "Running") {
		t.Errorf("--no-run ran something:\n%s", out.String())
	}
}

func TestTreeReadsTheHeadNotTheWorktree(t *testing.T) {
	dir, do, write := gitRepo(t)
	head := strings.TrimSpace(do("rev-parse", "HEAD"))
	write("api/internal/a/a.go", "uncommitted\n")
	tree, err := openTree(dir, head)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	b, err := tree.Read("api/internal/a/a.go")
	if err != nil || !strings.Contains(string(b), "func A") {
		t.Fatalf("read = %q, %v", b, err)
	}
	// twice, and a missing file
	if b2, _ := tree.Read("api/internal/a/a.go"); string(b2) != string(b) {
		t.Error("second read differs")
	}
	if _, err := tree.Read("nope.go"); err == nil {
		t.Error("missing file read")
	}
	if b3, err := tree.Read("docs/x.md"); err != nil || string(b3) != "# x\n" {
		t.Errorf("read after a miss = %q, %v", b3, err)
	}
}

func TestUsage(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"bogus"}, &out, &out); code != 2 {
		t.Errorf("unknown command: %d", code)
	}
	if code := run([]string{"gate", "--range", "nodots"}, &out, &out); code != 2 {
		t.Errorf("bad range: %d", code)
	}
}
