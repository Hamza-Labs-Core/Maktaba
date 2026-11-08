package appcontainer

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestOtherSystemsAreRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows runs the real code (plugins-sandbox)")
	}
	if _, err := Run(Profile, "x", nil, Options{}); err != ErrUnsupported {
		t.Fatalf("got %v", err)
	}
	if err := Grant(Profile, ".", false); err != ErrUnsupported {
		t.Fatalf("got %v", err)
	}
}

// Launch: no program after -- is a usage error; the supervisor's leading subcommand name is
// taken as aclaunch's arguments are; elsewhere than Windows the start is refused (exit 2).
func TestLaunchArguments(t *testing.T) {
	var out strings.Builder
	if code := Launch([]string{"plugins-sandbox-run", "-read", "."}, &out); code != 2 ||
		!strings.Contains(out.String(), "a program to run is required") {
		t.Fatalf("no program: %d %q", code, out.String())
	}
	if runtime.GOOS == "windows" {
		return
	}
	out.Reset()
	if code := Launch([]string{"plugins-sandbox-run", "--", "x"}, &out); code != 2 ||
		!strings.Contains(out.String(), ErrUnsupported.Error()) {
		t.Fatalf("not Windows: %d %q", code, out.String())
	}
}

// The plugin host's launcher grants each folder as the filesystem names it (a link above
// it resolved, on Windows short names expanded): Grant applies only to the object its path
// names, so a folder handed over by another spelling is canonicalized first, not refused;
// a folder that is itself a link is refused.
func TestThePluginHostsFoldersAreGrantedAsTheFilesystemNamesThem(t *testing.T) {
	dir := mustCanonical(t, t.TempDir())
	target := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	gf, rf := grantFn, runFn
	t.Cleanup(func() { grantFn, runFn = gf, rf })
	var granted []GrantEntry
	grantFn = func(_, path string, write bool) error {
		granted = append(granted, GrantEntry{Path: path, Write: write})
		return nil
	}
	runFn = func(string, string, []string, Options) (int, error) { return 0, nil }
	run := filepath.Join(link, "run")
	if code := Launch([]string{"plugins-sandbox-run", "-read", filepath.Join(link, "sub"), "-write", run, "--", "x"}, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := []GrantEntry{{Path: filepath.Join(target, "sub")}, {Path: filepath.Join(target, "run"), Write: true}}
	if !reflect.DeepEqual(granted, want) {
		t.Fatalf("granted %+v, want %+v", granted, want)
	}
	// a folder that is itself a link is refused, its target never granted
	granted = nil
	var out strings.Builder
	if code := Launch([]string{"plugins-sandbox-run", "-read", link, "--", "x"}, &out); code != 2 || len(granted) != 0 ||
		!strings.Contains(out.String(), "is a link") {
		t.Fatalf("a link: exit %d, granted %+v: %s", code, granted, out.String())
	}
}
