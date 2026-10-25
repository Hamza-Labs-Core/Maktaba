package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
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
		"login_item_unsigned: macOS refused the server's login item":      "login_item_unsigned",
		"relaunch_failed: the server did not come back":                   "app_install_failed",
		"signature mismatch": "app_install_failed",
	} {
		if code, _ := appFailure(msg); code != want {
			t.Errorf("%q: %s, want %s", msg, code, want)
		}
	}
	for code, refused := range map[string]bool{"needs_admin": true, "local_build": true, "app_management": true,
		"translocated": true, "not_writable": true, "installer_not_finished": false, "app_install_failed": false,
		// after the app swapped its bundle: the release before's copy is put back as for any install
		"login_item_unsigned": false} {
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
	j := n.waitState(StateCommitted)
	fr := n.m.Rehearser.(*fakeRehearser)
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.pruned) != 1 || fr.pruned[0] != 2 {
		t.Fatalf("pruned %v", fr.pruned)
	}
	// L1 (audit, second check): the dump the start took is the run's backup
	if j.Backup == nil || !strings.HasSuffix(j.Backup.Path, "pre-1.4.0-to-1.5.0.dump") || j.Backup.Bytes != 4096 {
		t.Fatalf("backup %+v", j.Backup)
	}
}

// H10 (audit): a release whose bundled Postgres is another major than the running one is
// refused before it is installed, a person's to mend (pg_major_change); the same major,
// or one not known, goes on.
func TestPGMajorChangeRefused(t *testing.T) {
	pay := func(pg string) apppayload.Payload {
		var p apppayload.Payload
		p.Postgres.Version = pg
		return p
	}
	err := pgMajorChange(pay("16.15.0"), pay("17.2.0"), "1.5.0")
	var f *Failure
	if !errors.As(err, &f) || f.Code != "pg_major_change" || !strings.Contains(f.Detail, "PostgreSQL 17") {
		t.Fatalf("a major change: %v", err)
	}
	for _, c := range [][2]string{{"16.15.0", "16.16.0"}, {"", "17.0"}, {"16.1", ""}} {
		if err := pgMajorChange(pay(c[0]), pay(c[1]), "1.5.0"); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
}

// B6 (audit): `current` moves to a runtime of another Python minor only once the Python
// processes stopped; the same minor stops nothing.
func TestRuntimeSwitchStopsPythonForAnotherMinor(t *testing.T) {
	root := t.TempDir()
	for name, py := range map[string]string{"r-1.4.0": "3.12.7", "r-1.5.0": "3.13.1", "r-1.5.1": "3.12.9"} {
		_ = os.MkdirAll(filepath.Join(root, name), 0o755)
		_ = os.WriteFile(filepath.Join(root, name, "payload.json"),
			[]byte(`{"schema":1,"version":"x","python":{"version":"`+py+`"},"postgres":{"version":"16.15.0"}}`), 0o644)
	}
	stops, starts := 0, 0
	ri := &RuntimeInstaller{Runtime: appruntime.New(root), LauncherRoot: t.TempDir(),
		StopPython:  func(context.Context) error { stops++; return nil },
		StartPython: func(context.Context) error { starts++; return nil }}
	j := &Journal{Job: Job{RunID: "run-1", Version: "1.5.0"}, FromRef: "r-1.4.0", Staged: "r-1.5.0"}
	_ = ri.Switch(context.Background(), j) // the directories are no installed runtimes: Point refuses them
	if stops != 1 {
		t.Fatalf("a Python minor change: %d stops", stops)
	}
	// the switch went no further (Point refused): the pipeline is back on
	if starts != 1 {
		t.Fatalf("the pipeline not turned back on after a failed switch: %d starts", starts)
	}
	j.Staged = "r-1.5.1"
	_ = ri.Switch(context.Background(), j)
	if stops != 1 || starts != 1 {
		t.Fatalf("the same minor stopped or started the pipeline: %d stops, %d starts", stops, starts)
	}
}

// An app bundle whose release says its PostgreSQL is another major than the running
// payload's is refused before the app installs it (pg_major_change, a person's); a release
// that does not say, or the same major, goes on.
func TestAppPGMajorChange(t *testing.T) {
	var have apppayload.Payload
	have.Postgres.Version = "16.15.0"
	res := func(major int) *releaseverify.Result {
		return &releaseverify.Result{Manifest: &releaseverify.Manifest{AppPostgresMajor: map[string]int{appPayloadKind(): major}}}
	}
	err := appPGMajorChange(have, res(17), "0.4.0")
	var f *Failure
	if !errors.As(err, &f) || f.Code != "pg_major_change" || !strings.Contains(f.Error(), "PostgreSQL 17") {
		t.Fatalf("another major: %v", err)
	}
	if err := appPGMajorChange(have, res(16), "0.4.0"); err != nil {
		t.Fatalf("the same major: %v", err)
	}
	if err := appPGMajorChange(have, &releaseverify.Result{Manifest: &releaseverify.Manifest{}}, "0.4.0"); err != nil {
		t.Fatalf("a release that does not say: %v", err)
	}
	if err := appPGMajorChange(apppayload.Payload{}, res(17), "0.4.0"); err != nil {
		t.Fatalf("a payload that does not say: %v", err)
	}
	if err := appPGMajorChange(have, nil, "0.4.0"); err != nil {
		t.Fatalf("no result: %v", err)
	}
}

// The job carries the release's app PostgreSQL majors (the home's verified catalog): a
// preflight refuses another major than the running payload's before any download.
func TestJobPGMajorChange(t *testing.T) {
	var have apppayload.Payload
	have.Postgres.Version = "16.15.0"
	err := jobPGMajorChange(have, map[string]int{appPayloadKind(): 17}, "0.4.0")
	var f *Failure
	if !errors.As(err, &f) || f.Code != "pg_major_change" {
		t.Fatalf("another major: %v", err)
	}
	if err := jobPGMajorChange(have, map[string]int{appPayloadKind(): 16}, "0.4.0"); err != nil {
		t.Fatalf("the same major: %v", err)
	}
	if err := jobPGMajorChange(have, nil, "0.4.0"); err != nil {
		t.Fatalf("a job that does not say: %v", err)
	}
}

// The Docker install's runtime updates as a headless install's does; its kind says Docker
// (the Updates page names it).
func TestRuntimeInstallerKindInTheDockerImage(t *testing.T) {
	if k := (&RuntimeInstaller{}).Kind(); k != "runtime" {
		t.Errorf("a Linux runtime: %q", k)
	}
	if k := (&RuntimeInstaller{Container: "docker"}).Kind(); k != "docker" {
		t.Errorf("the Docker image's runtime: %q", k)
	}
}
