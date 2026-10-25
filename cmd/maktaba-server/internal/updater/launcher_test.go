package updater

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Story 35.7 AC6 (review B-M9): a release that needs a newer launcher puts it in place
// after its commit and asks the running launcher to hand over; one this install cannot
// bring its launcher to is refused before it installs.
func TestLauncherReplacement(t *testing.T) {
	root := t.TempDir()
	l := layout.New(root)
	rel := l.ReleaseDir("1.5.0")
	_ = os.MkdirAll(rel, 0o755)
	_ = os.WriteFile(filepath.Join(rel, layout.Exe("maktaba-launcher")), []byte("launcher 2"), 0o755)
	exe := filepath.Join(root, "bin", layout.Exe("maktaba-launcher"))
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	_ = os.WriteFile(exe, []byte("launcher 1"), 0o755)
	t.Setenv(layout.EnvLauncherProtocol, "1")
	t.Setenv(layout.EnvLauncherExe, exe)
	t.Setenv(layout.EnvLauncherHandover, "1") // a launcher that keeps the TV across restarts
	li := &LayoutInstaller{Layout: l, Version: "1.4.0"}
	j := &Journal{Job: homeJob(), LauncherProtocol: 1}

	// the same protocol: nothing to do, nothing refused
	if replaced, err := li.ReplaceLauncher(j); replaced || err != nil {
		t.Fatalf("protocol 1 under 1: %v %v", replaced, err)
	}
	ok, why := li.launcherReplaceable(j.Job.Version)
	if err := checkLauncher(j, ok, why); err != nil {
		t.Fatal(err)
	}

	j.LauncherProtocol = 2
	if runtime.GOOS == "windows" {
		var f *Failure
		if err := checkLauncher(j, ok, why); !errors.As(err, &f) || f.Code != "launcher_protocol" {
			t.Fatalf("Windows cannot hand over in place: %v", err)
		}
		// the refusal names the command that does it by hand (review round 2)
		next := filepath.Join(l.ReleaseDir(layout.NormalizeVersion(j.Job.Version)), layout.Exe("maktaba-launcher"))
		if !strings.Contains(f.Detail, `"`+next+`" --replace-self "`+exe+`"`) || !strings.Contains(f.Detail, "Restart-Service Maktaba") {
			t.Fatalf("the refusal names no command: %s", f.Detail)
		}
		return
	}
	if err := checkLauncher(j, ok, why); err != nil {
		t.Fatalf("a replaceable launcher: %v", err)
	}
	prev := replaceSelf
	defer func() { replaceSelf = prev }()
	replaceSelf = func(newLauncher, target string) error { return os.Rename(newLauncher, target) }
	replaced, err := li.ReplaceLauncher(j)
	if !replaced || err != nil {
		t.Fatalf("replace: %v %v", replaced, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "launcher 2" {
		t.Fatalf("the launcher in place says %q", b)
	}
	req, _ := os.ReadFile(filepath.Join(l.UpdatesDir(), layout.ReplaceLauncherFile))
	if strings.TrimSpace(string(req)) != exe {
		t.Fatalf("handover request %q", req)
	}
	// a package's launcher (EC4) is not replaced: the release is refused
	if !launcherPackaged("/usr/bin/maktaba-launcher") || launcherPackaged(exe) {
		t.Fatal("packaged launcher detection")
	}
}

// After the commit the machine hands over to the new launcher (exit 75), the step on the
// run's record; a release with the running protocol commits without it.
func TestCommitHandsOverToANewLauncher(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.mutate(func(j *Journal) { j.LauncherProtocol = 3 })
	_ = r.m.Apply("run-1")
	r.waitExit()
	n := startRig(t, r.dir, w, healthy)
	n.inst.replaces = true
	r.cancel()
	n.m.Resume()
	n.waitState(StateCommitted)
	n.waitExit()
	j := n.m.Status().Journal
	found := false
	for _, s := range j.Steps {
		if s.Step == StepLauncher && s.State == "done" {
			found = true
		}
	}
	if !found || !w.called("launcher") {
		t.Fatalf("steps %+v", j.Steps)
	}
}

// Review round 2: the handover's restart passes no TV gate, so a machine keeping TV on
// air does not restart for it: the new launcher takes over at the next restart.
func TestHandoverWaitsWhileTVIsOnAir(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.mutate(func(j *Journal) { j.LauncherProtocol = 3 })
	_ = r.m.Apply("run-1")
	r.waitExit()
	n := startRig(t, r.dir, w, healthy)
	n.inst.replaces = true
	n.m.TVOnAir = func() bool { return true }
	r.cancel()
	n.m.Resume()
	n.waitState(StateCommitted)
	deadline := time.Now().Add(5 * time.Second)
	for !w.called("launcher") && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	n.noExit()
	j := n.m.Status().Journal
	for _, s := range j.Steps {
		if s.Step == StepLauncher && s.State == "done" && strings.Contains(s.Detail, "next restart") {
			return
		}
	}
	t.Fatalf("steps %+v", j.Steps)
}

// Story 35.7: a launcher from before the TV handover (it does not say
// MAKTABA_LAUNCHER_HANDOVER) is replaced by the committed release's on POSIX even at the
// same protocol, where that happens by itself; one that cannot be replaced stays, and
// nothing is refused for it. Windows keeps any launcher.
func TestLauncherRefreshForTheHandover(t *testing.T) {
	root := t.TempDir()
	l := layout.New(root)
	rel := l.ReleaseDir("1.5.0")
	_ = os.MkdirAll(rel, 0o755)
	_ = os.WriteFile(filepath.Join(rel, layout.Exe("maktaba-launcher")), []byte("launcher with the handover"), 0o755)
	exe := filepath.Join(root, "bin", layout.Exe("maktaba-launcher"))
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	_ = os.WriteFile(exe, []byte("launcher 1"), 0o755)
	t.Setenv(layout.EnvLauncherProtocol, "1")
	t.Setenv(layout.EnvLauncherExe, exe)
	t.Setenv(layout.EnvLauncherHandover, "")
	li := &LayoutInstaller{Layout: l, Version: "1.4.0"}
	j := &Journal{Job: homeJob(), LauncherProtocol: 1}
	ok, why := li.launcherReplaceable(j.Job.Version)
	if err := checkLauncher(j, ok, why); err != nil {
		t.Fatalf("a refresh is never refused: %v", err)
	}
	prev := replaceSelf
	defer func() { replaceSelf = prev }()
	replaceSelf = func(newLauncher, target string) error { return os.Rename(newLauncher, target) }
	replaced, err := li.ReplaceLauncher(j)
	if runtime.GOOS == "windows" {
		if replaced || err != nil || refreshLauncher() {
			t.Fatalf("Windows keeps its launcher: %v %v", replaced, err)
		}
		return
	}
	if !replaced || err != nil {
		t.Fatalf("refresh: %v %v", replaced, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "launcher with the handover" {
		t.Fatalf("the launcher in place says %q", b)
	}
	// a package's launcher stays, without an error (EC4)
	t.Setenv(layout.EnvLauncherExe, "/usr/bin/maktaba-launcher")
	if replaced, err := li.ReplaceLauncher(j); replaced || err != nil {
		t.Fatalf("a package's launcher: %v %v", replaced, err)
	}
	// one that says it keeps the TV: nothing to do
	t.Setenv(layout.EnvLauncherExe, exe)
	t.Setenv(layout.EnvLauncherHandover, "1")
	if replaced, err := li.ReplaceLauncher(j); replaced || err != nil {
		t.Fatalf("a launcher with the handover: %v %v", replaced, err)
	}
}
