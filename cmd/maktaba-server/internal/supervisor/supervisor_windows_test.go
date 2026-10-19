//go:build windows

package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

// On Windows a role binary is found by its .exe name, with no execute bits to check.
func TestResolveBinaryFindsTheExeOnWindows(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "maktaba-api.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveBinary("maktaba-api", "MAKTABA_TEST_NO_SUCH_BIN", dir)
	if err != nil || got != exe {
		t.Fatalf("resolveBinary = %q, %v; want %q", got, err, exe)
	}
	t.Setenv("MAKTABA_TEST_BIN", exe)
	if got, err := resolveBinary("maktaba-api", "MAKTABA_TEST_BIN", ""); err != nil || got != exe {
		t.Fatalf("override: %q, %v", got, err)
	}
}
