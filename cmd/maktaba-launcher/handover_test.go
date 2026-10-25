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
	time.Sleep(300 * time.Millisecond) // past the launcher's release of the first server
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
