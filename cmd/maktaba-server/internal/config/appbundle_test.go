package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppBundle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAKTABA_APP_BUNDLE", dir)
	if got := AppBundle(); got != dir {
		t.Errorf("AppBundle = %q, want %q", got, dir)
	}
	for _, v := range []string{"", "0", filepath.Join(dir, "missing")} {
		t.Setenv("MAKTABA_APP_BUNDLE", v)
		if got := AppBundle(); got != "" {
			t.Errorf("MAKTABA_APP_BUNDLE=%q: AppBundle = %q, want none", v, got)
		}
	}
	// "1": the payload holding this executable (<payload>/bin/<exe>)
	t.Setenv("MAKTABA_APP_BUNDLE", "1")
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if got := AppBundle(); got != filepath.Dir(filepath.Dir(exe)) {
		t.Errorf("AppBundle for 1 = %q", got)
	}
}
