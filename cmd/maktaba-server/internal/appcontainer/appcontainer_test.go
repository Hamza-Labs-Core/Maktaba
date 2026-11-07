package appcontainer

import (
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
