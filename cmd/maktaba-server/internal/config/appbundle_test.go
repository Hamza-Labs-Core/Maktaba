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

// The app's payload: $MAKTABA_APP_BUNDLE (the launcher), or payload.json above
// the bin of the executable (a setup step the app runs directly), links resolved.
func TestInAppPayload(t *testing.T) {
	payload := filepath.Join(t.TempDir(), "server")
	exe := filepath.Join(payload, "bin", "maktaba-server.exe")
	writeAt(t, exe, "exe")
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	if InAppPayload(exe) || InAppPayload("") {
		t.Fatal("no payload.json and no MAKTABA_APP_BUNDLE: not the app")
	}
	t.Setenv("MAKTABA_APP_BUNDLE", payload)
	if !InAppPayload("") {
		t.Fatal("MAKTABA_APP_BUNDLE names the payload")
	}
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	writeAt(t, filepath.Join(payload, "payload.json"), "{}")
	if !InAppPayload(exe) {
		t.Fatal("payload.json above bin: the app")
	}
	link := filepath.Join(t.TempDir(), "maktaba-server.exe")
	if err := os.Symlink(exe, link); err != nil {
		t.Logf("no link here (%v): the link case is not checked", err)
		return
	}
	if !InAppPayload(link) {
		t.Fatal("a link to the payload's server: the app (the link resolved)")
	}
}
