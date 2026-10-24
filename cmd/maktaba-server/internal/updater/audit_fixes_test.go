package updater

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The app's own codes for an order it could not install reach the run as themselves (the
// home's driver tells a person's failure from a release's); one it refused before it
// touched its bundle changed nothing (the release before needs no copy put back).
func TestAppFailureCodes(t *testing.T) {
	for msg, want := range map[string]string{
		"needs_admin: /Applications is not this user's":                   "needs_admin",
		"local_build: this app was built locally and can't update itself": "local_build",
		"app_management: macOS's App Management refused the update":       "app_management",
		"installer_not_finished: the installer did not finish":            "installer_not_finished",
		"translocated: run from a disk image":                             "translocated",
		"not_writable: the app's folder cannot be changed":                "not_writable",
		"signature mismatch": "app_install_failed",
	} {
		if code, _ := appFailure(msg); code != want {
			t.Errorf("%q: %s, want %s", msg, code, want)
		}
	}
	for code, refused := range map[string]bool{"needs_admin": true, "local_build": true, "app_management": true,
		"translocated": true, "not_writable": true, "installer_not_finished": false, "app_install_failed": false} {
		if refusedCodes[code] != refused {
			t.Errorf("%s refused: %v", code, refusedCodes[code])
		}
	}
}

// L2 (audit): a layout node on Windows never builds its venv with the Microsoft Store's
// python3 alias: another Python on PATH is taken, and with only the alias the run says
// so (python_store_stub, a person's), not a venv_failed that counts against the release.
func TestPythonSkipsTheStoreAlias(t *testing.T) {
	stub := filepath.FromSlash("C:/Users/u/AppData/Local/Microsoft/WindowsApps/python3.exe")
	py312 := filepath.FromSlash("C:/Python312/python.exe")
	oldLook, oldPy := lookPath, pyLauncher
	t.Cleanup(func() { lookPath, pyLauncher = oldLook, oldPy })
	pyLauncher = func() string { return "" }
	t.Setenv(envPipelinePython, "")
	li := &LayoutInstaller{Layout: layout.New(t.TempDir()), Version: "1.4.0"}
	lookPath = func(name string) (string, error) {
		switch name {
		case "python3":
			return stub, nil
		case "python":
			return py312, nil
		}
		return "", errors.New("not found")
	}
	if py, s := li.python(); py != py312 || s != "" {
		t.Fatalf("python %q stub %q", py, s)
	}
	lookPath = func(name string) (string, error) {
		if name == "python3" {
			return stub, nil
		}
		return "", errors.New("not found")
	}
	if py, s := li.python(); py != "" || s != stub {
		t.Fatalf("only the alias: python %q stub %q", py, s)
	}
	if !storeAlias(stub) || storeAlias(py312) {
		t.Fatal("storeAlias")
	}
}

// L1 (audit): a home's commit keeps its dumps to rules.updates.backups_keep.
func TestCommitPrunesTheBackups(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	job := homeJob()
	job.BackupsKeep = 2
	if err := r.m.Prepare(job); err != nil {
		t.Fatal(err)
	}
	r.waitState(StatePrepared)
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitExit()
	r.waitState(StateRestarting)
	w.migrated = true
	n := r.restart(healthy)
	n.waitState(StateCommitted)
	fr := n.m.Rehearser.(*fakeRehearser)
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.pruned) != 1 || fr.pruned[0] != 2 {
		t.Fatalf("pruned %v", fr.pruned)
	}
}
