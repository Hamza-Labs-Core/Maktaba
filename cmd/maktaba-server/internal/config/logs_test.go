package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A logs directory and a log something made wider first end up the user's only.
func TestOpenLogIsTheUsersOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "venv.log"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLogIn(dir, "venv.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("new\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if fi, _ := os.Stat(dir); fi == nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("logs dir mode %v", fi)
	}
	fi, _ := os.Stat(filepath.Join(dir, "venv.log"))
	if fi == nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v", fi)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "venv.log")); string(b) != "old\nnew\n" {
		t.Errorf("appended %q", b)
	}
	// a fresh one
	f, err = openLogIn(filepath.Join(t.TempDir(), "a", "logs"), "ollama.log")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// A log at a path: the file is the user's only; a folder it makes is too, and
// a folder that was there keeps its mode (it may be anybody's).
func TestOpenLogFileLeavesAFolderItDidNotMake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := OpenLogFile(filepath.Join(shared, "ollama.log"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if fi, _ := os.Stat(shared); fi == nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("an existing folder's mode changed: %v", fi)
	}
	if fi, _ := os.Stat(filepath.Join(shared, "ollama.log")); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v", fi)
	}
	made := filepath.Join(shared, "new", "logs")
	f, err = OpenLogFile(filepath.Join(made, "ollama.log"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if fi, _ := os.Stat(made); fi == nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("a folder it made: %v", fi)
	}
}
