package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUsage(t *testing.T) {
	if run(nil) != 2 || run([]string{"-dir", "x"}) != 2 {
		t.Fatal("no -dir or -version must be a usage error")
	}
}

func TestNoChecksumsFails(t *testing.T) {
	if run([]string{"-dir", t.TempDir(), "-version", "1.0.0"}) != 1 {
		t.Fatal("a directory with no checksums.txt passed")
	}
}

// The release's run: build.py's output, checksummed, all trusted; and one changed byte fails.
func TestARelease(t *testing.T) {
	if testing.Short() {
		t.Skip("builds every first-party package")
	}
	dir := t.TempDir()
	cmd := exec.Command("python3", filepath.Join("..", "..", "..", "tools", "plugins", "build.py"), "--mkplugin", packer(t), "--version", "v1.0.0", "--out", dir)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build.py: %v: %s", err, b)
	}
	sums := func() {
		entries, _ := os.ReadDir(dir)
		var b strings.Builder
		for _, e := range entries {
			if e.Name() == "checksums.txt" {
				continue
			}
			raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			s := sha256.Sum256(raw)
			fmt.Fprintf(&b, "%s  ./%s\n", hex.EncodeToString(s[:]), e.Name())
		}
		_ = os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(b.String()), 0o644)
	}
	sums()
	if code := run([]string{"-dir", dir, "-version", "v1.0.0"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	fj, _ := filepath.Glob(filepath.Join(dir, "*.files.json"))
	b, _ := os.ReadFile(fj[0])
	b[1] ^= 1
	_ = os.WriteFile(fj[0], b, 0o644)
	if code := run([]string{"-dir", dir, "-version", "1.0.0"}); code != 1 {
		t.Fatalf("a changed files.json: exit %d", code)
	}
}

// packer builds the home's .mkplugin writer from this module's own graph (api requires
// componentpack), so the test needs no module beyond api's.
func packer(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "mkplugin")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, "github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack/cmd/mkplugin")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build mkplugin: %v: %s", err, b)
	}
	return exe
}
