package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Story 35.7 TC5: the TV process a server hands over across its exit 75 runs on while
// the next server starts; at the launcher's stop it is stopped (the record names it),
// and the record goes.
func TestHandoverKeptAcrossTheRestartAndStoppedAtTheStop(t *testing.T) {
	processTest(t)
	if layout.BootID() == "" {
		t.Skip("no boot id here: no handover is ever taken")
	}
	l := installed(t, map[string]string{"1.0.0": "handover75,run"}, "1.0.0")
	cfg := testConfig(l.Root)
	stop := startLauncher(t, cfg)
	waitFor(t, "the next server", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	b, err := os.ReadFile(filepath.Join(l.Root, "tv.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	rec, _ := layout.ReadHandover(l.UpdatesDir())
	if rec == nil || len(rec.Children) != 1 || rec.Children[0].PID != pid {
		t.Fatalf("record %+v", rec)
	}
	start := rec.Children[0].Start
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil && layout.ProcessIs(pid, start) {
			_ = p.Kill()
		}
	})
	// the next server ran: the launcher's release of the first one is behind
	if !layout.ProcessIs(pid, start) {
		t.Fatal("the TV process handed over was stopped at the restart")
	}
	if n := len(events(l, "launcher_handover")); n != 1 {
		t.Fatalf("launcher_handover events = %d", n)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for layout.ProcessIs(pid, start) {
		if time.Now().After(deadline) {
			t.Fatal("the TV process runs on after the launcher stopped")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r, _ := layout.ReadHandover(l.UpdatesDir()); r != nil {
		t.Fatalf("the record outlived the stop: %+v", r)
	}
}

// handoverRelease marks a release's supervisor as one that adopts (its release.json's
// handover_protocol).
func handoverRelease(t *testing.T, l layout.Layout, v string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.ReleaseDir(v), "release.json"),
		[]byte(`{"v":1,"handover_protocol":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Story 35.7 (review M2): the release under trial that took the TV over crashes: the
// TV runs on for the next start (the release again, or the one the guard goes back to),
// both of whose supervisors adopt; at the stop it is stopped.
func TestHandoverKeptAcrossACrashUnderTrial(t *testing.T) {
	processTest(t)
	if layout.BootID() == "" {
		t.Skip("no boot id here: no handover is ever taken")
	}
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "handover75,crash,run"}, "1.0.0")
	handoverRelease(t, l, "1.0.0")
	handoverRelease(t, l, "2.0.0")
	if err := l.Switch("2.0.0", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	stop := startLauncher(t, testConfig(l.Root))
	waitFor(t, "the start after the crash", func() bool { return countAct(readTrace(l), "2.0.0", "run") == 1 })
	b, err := os.ReadFile(filepath.Join(l.Root, "tv.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	rec, _ := layout.ReadHandover(l.UpdatesDir())
	if rec == nil || len(rec.Children) != 1 || rec.Children[0].PID != pid {
		t.Fatalf("record %+v", rec)
	}
	start := rec.Children[0].Start
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil && layout.ProcessIs(pid, start) {
			_ = p.Kill()
		}
	})
	if !layout.ProcessIs(pid, start) {
		t.Fatal("the crash of the release under trial stopped the TV handed over to it")
	}
	if n := len(events(l, "launcher_handover_kept")); n != 1 {
		t.Fatalf("launcher_handover_kept events = %d", n)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for layout.ProcessIs(pid, start) {
		if time.Now().After(deadline) {
			t.Fatal("the TV process runs on after the launcher stopped")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The TV is kept for the next start only when both releases it may be adopt.
func TestNextAdopts(t *testing.T) {
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
	if err := l.Switch("2.0.0", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	ln := &launcher{l: l}
	handoverRelease(t, l, "2.0.0")
	if ln.nextAdopts() {
		t.Fatal("the release the guard goes back to does not adopt, yet the TV was kept for it")
	}
	handoverRelease(t, l, "1.0.0")
	if !ln.nextAdopts() {
		t.Fatal("both adopt")
	}
}
