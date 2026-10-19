package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Story 33.2 TC4 (TestSupervisorChecksumRefusal): a slot's binary must be its
// release's - the right file in bin/releases/<version>/ with the recorded SHA-256.
func TestSupervisorChecksumRefusal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "releases", "0.42.1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("streaming 0.42.1")
	if err := os.WriteFile(filepath.Join(dir, streamingExe()), body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	got, err := ResolveReleaseBinary(root, "0.42.1", want, "0.43.0", "")
	if err != nil || got.SHA256 != want || filepath.Dir(got.Path) != dir {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if _, err := ResolveReleaseBinary(root, "0.42.1", "00"+want[2:], "0.43.0", ""); !errors.Is(err, ErrReleaseChecksum) {
		t.Fatalf("a wrong checksum must be refused: %v", err)
	}
	if _, err := ResolveReleaseBinary(root, "0.40.0", "", "0.43.0", ""); !errors.Is(err, ErrReleaseBinaryMissing) {
		t.Fatalf("a missing version: %v", err)
	}
	if _, err := ResolveReleaseBinary(root, "../x", "", "", ""); !errors.Is(err, ErrReleaseBinaryMissing) {
		t.Fatalf("a path in the version: %v", err)
	}
	// release 1 before side-by-side installs: the running binary
	cur := filepath.Join(root, "current")
	if err := os.WriteFile(cur, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveReleaseBinary(root, "0.43.0", "", "0.43.0", cur)
	if err != nil || got.Path != cur {
		t.Fatalf("fallback: %+v %v", got, err)
	}
}
