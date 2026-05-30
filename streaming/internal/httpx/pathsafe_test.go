package httpx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCanonicalUnder_AllowsLegitimateSubPath(t *testing.T) {
	base := t.TempDir()
	got, err := CanonicalUnder(base, "720p", "seg-1.ts")
	if err != nil {
		t.Fatalf("unexpected error for legit path: %v", err)
	}
	want := filepath.Join(base, "720p", "seg-1.ts")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCanonicalUnder_RejectsDotDotTraversal(t *testing.T) {
	base := t.TempDir()
	cases := [][]string{
		{"..", "etc", "passwd"},
		{"720p", "..", "..", "..", "etc", "passwd"},
		{"../../../../etc/passwd"},
		{"720p/../../secret"},
	}
	for _, parts := range cases {
		if _, err := CanonicalUnder(base, parts...); err != ErrUnsafePath {
			t.Errorf("parts=%v: err=%v, want ErrUnsafePath", parts, err)
		}
	}
}

func TestCanonicalUnder_RejectsAbsoluteSegment(t *testing.T) {
	base := t.TempDir()
	if _, err := CanonicalUnder(base, "/etc/passwd"); err != ErrUnsafePath {
		t.Errorf("absolute segment: err=%v, want ErrUnsafePath", err)
	}
}

func TestCanonicalUnder_RejectsNUL(t *testing.T) {
	base := t.TempDir()
	if _, err := CanonicalUnder(base, "seg\x00.ts"); err != ErrUnsafePath {
		t.Errorf("NUL segment: err=%v, want ErrUnsafePath", err)
	}
}

func TestCanonicalUnder_RejectsSiblingPrefixTrick(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "cache")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// "cache-evil" shares the textual prefix "cache" but is a sibling,
	// not a descendant. Joining "../cache-evil/x" must be rejected.
	if _, err := CanonicalUnder(base, "..", "cache-evil", "x"); err != ErrUnsafePath {
		t.Errorf("sibling-prefix: err=%v, want ErrUnsafePath", err)
	}
}

func TestCanonicalUnder_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	parent := t.TempDir()
	base := filepath.Join(parent, "cache")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	secretDir := filepath.Join(parent, "secret")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(secretDir, "passwd")
	if err := os.WriteFile(secretFile, []byte("root:x:0:0"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Plant a symlink INSIDE the cache that points outside it.
	link := filepath.Join(base, "escape")
	if err := os.Symlink(secretDir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalUnder(base, "escape", "passwd"); err != ErrUnsafePath {
		t.Errorf("symlink escape: err=%v, want ErrUnsafePath", err)
	}
}

func TestCanonicalUnder_AllowsNotYetExistingLeaf(t *testing.T) {
	base := t.TempDir()
	// FFmpeg hasn't written this segment yet — must pass the gate so
	// the caller's os.ReadFile produces the normal 404.
	got, err := CanonicalUnder(base, "1080p", "seg-99.ts")
	if err != nil {
		t.Fatalf("not-yet-existing leaf rejected: %v", err)
	}
	if got != filepath.Join(base, "1080p", "seg-99.ts") {
		t.Fatalf("got %q", got)
	}
}
