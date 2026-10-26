package updater

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// logBuf is a server log the machine writes from its goroutines.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *logBuf) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(l.String(), want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("log:\n%s\nwant a line with %q", l.String(), want)
}

// a switch the machine undoes and whose undo fails says so, at level error; the run
// log (the Updates page) keeps the line without its level.
func TestAFailedUndoOfTheSwitchIsLogged(t *testing.T) {
	w := newWorld("1.4.0")
	dir := t.TempDir()
	r := startRig(t, dir, w, healthy)
	r.inst.dieAt = "before:" + StepSwitch
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	r.waitState(StatePrepared)
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	<-r.inst.died
	r.cancel()
	n := startRig(t, dir, w, healthy)
	n.inst.failAt = "revert"
	var log logBuf
	n.m.Log = &log
	n.m.Resume()
	n.waitState(StateApplyFailed)
	log.waitFor(t, "[update] level=error undoing the switch after an interrupted apply: ")
	log.waitFor(t, "[update] level=warn the apply of 1.5.0 was interrupted")
	run, err := os.ReadFile(filepath.Join(dir, "logs", "run-1.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(run), "[update] undoing the switch after an interrupted apply: ") ||
		strings.Contains(string(run), "level=") {
		t.Fatalf("run log:\n%s", run)
	}
}

// a journal that cannot be written is logged once per distinct failure (most callers drop
// the error), and its recovery once.
func TestAJournalWriteFailureIsLoggedOnce(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var log logBuf
	m := &Machine{Dir: filepath.Join(blocker, "updates"), Log: &log}
	m.j = &Journal{Job: Job{RunID: "run-1"}}
	for i := 0; i < 3; i++ {
		if err := m.mutate(func(*Journal) {}); err == nil {
			t.Fatal("a journal under a file was written")
		}
	}
	if got := strings.Count(log.String(), "[update] level=error cannot write the journal: "); got != 1 {
		t.Fatalf("logged %d times:\n%s", got, log.String())
	}
	m.Dir = t.TempDir()
	if err := m.mutate(func(*Journal) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(log.String(), "[update] the journal is written again\n") {
		t.Fatalf("log:\n%s", log.String())
	}
}
