package testtier

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssertNoTmpLeaksClean(t *testing.T) {
	dir := t.TempDir()
	pattern := filepath.Join(dir, "maktaba-*")

	var buf bytes.Buffer
	got := AssertNoTmpLeaks(&buf, pattern, 0)
	if got != 0 {
		t.Fatalf("expected exit 0 with no leaks, got %d (out=%q)", got, buf.String())
	}
	if buf.Len() != 0 {
		t.Fatalf("expected silent on clean sweep, got %q", buf.String())
	}
}

func TestAssertNoTmpLeaksDetectsLeak(t *testing.T) {
	dir := t.TempDir()
	leaked := filepath.Join(dir, "maktaba-12345")
	if err := os.Mkdir(leaked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pattern := filepath.Join(dir, "maktaba-*")

	var buf bytes.Buffer
	got := AssertNoTmpLeaks(&buf, pattern, 0)
	if got != 1 {
		t.Fatalf("expected exit 1 with leak, got %d", got)
	}
	if !strings.Contains(buf.String(), "maktaba-12345") {
		t.Fatalf("expected leak path in output, got %q", buf.String())
	}
}

func TestAssertNoTmpLeaksPreservesFailureExit(t *testing.T) {
	dir := t.TempDir()
	pattern := filepath.Join(dir, "maktaba-*")

	var buf bytes.Buffer
	got := AssertNoTmpLeaks(&buf, pattern, 7)
	if got != 7 {
		t.Fatalf("expected upstream exit 7 to pass through, got %d", got)
	}
}
