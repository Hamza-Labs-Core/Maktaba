package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
)

// copyTestBinary copies the test binary (the fake child) to dir/name: a release's
// streaming binary another release's supervisor finds kept.
func copyTestBinary(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// heldSlot is a fake TV slot (the test binary serving /healthz) asking held which
// streaming software the live TV is held on.
func heldSlot(t *testing.T, instance string, held func(context.Context) (string, error)) Process {
	t.Helper()
	p, _ := fakeProc(t, RoleTVSlot, "serve")
	p.Instance, p.Survives, p.Held = instance, false, held
	return p
}

// runningPath is the binary c's process runs now ("" when it runs none).
func runningPath(s *Supervisor, instance string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.proc.Instance == instance && c.cmd != nil && c.status.PID > 0 {
			return c.cmd.Path
		}
	}
	return ""
}

func slotRunning(instance string) func(Snapshot) bool {
	return func(snap Snapshot) bool {
		for _, p := range snap.Processes {
			if p.Role == RoleTVSlot && p.Instance == instance && p.State == StateRunning {
				return true
			}
		}
		return false
	}
}

// The live TV held on 1.4.0 (its update's TV check failed): the supervisor of 1.5.0 asks
// once which streaming software the live TV runs, and the slots the front door has not bound
// (the live slot and the standby started at boot) run 1.4.0's binary where the install keeps
// it, restarts included, and that release counts as in use (no commit prunes it). A slot the
// front door binds to a release runs that release's.
func TestHeldTVSlotsRunTheKeptBinary(t *testing.T) {
	old := version.Version
	version.Version = "v1.5.0"
	t.Cleanup(func() { version.Version = old })

	kept := copyTestBinary(t, filepath.Join(t.TempDir(), "payloads", "1.4.0", "bin"), "maktaba-streaming")
	finder := func(v string) []string {
		if bareVersion(v) == "1.4.0" {
			return []string{kept}
		}
		return nil
	}
	var asked atomic.Int32
	held := func(context.Context) (string, error) { asked.Add(1); return "v1.4.0", nil }
	green, blue := heldSlot(t, "green", held), heldSlot(t, "blue", held)
	base := green.Cmd.Path
	log := &syncWriter{w: &bytes.Buffer{}}
	s := New([]Process{green, blue}, testPolicy(), WithKeptStreaming(finder), WithLog(log))
	// the front door bound the standby to a release (its binary: this one's)
	if err := s.Bind("blue", &SlotBinding{ReleaseID: "r-new", Version: "v1.5.0"}); err != nil {
		t.Fatal(err)
	}
	stop, _ := running(t, s)
	waitFor(t, s, "green running", slotRunning("green"))
	waitFor(t, s, "blue running", slotRunning("blue"))
	if got := runningPath(s, "green"); got != kept {
		t.Fatalf("the live slot runs %s, want the held release's kept binary %s", got, kept)
	}
	if got := runningPath(s, "blue"); got != base {
		t.Fatalf("the bound standby runs %s, want its release's %s", got, base)
	}
	if v, p := s.HeldStreaming(); v != "v1.4.0" || p != kept {
		t.Fatalf("HeldStreaming = %s %s", v, p)
	}
	// the held 1.4.0, and 1.5.0 the standby is bound to
	if got := strings.Join(s.ReleasesInUse(), ","); got != "1.4.0,1.5.0" {
		t.Fatalf("in use %s, want the held 1.4.0 too", got)
	}
	// a crash restarts it on the held release, not this one's
	s.mu.Lock()
	pid := s.children[0].status.PID
	s.mu.Unlock()
	get(t, strings.TrimSuffix(green.Probe, "/healthz")+"/crash?code=3")
	waitFor(t, s, "green restarted", func(snap Snapshot) bool {
		return slotRunning("green")(snap) && snap.Processes[0].PID != pid
	})
	if got := runningPath(s, "green"); got != kept {
		t.Fatalf("restarted on %s, want %s", got, kept)
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked %d times, want once", n)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "the live TV is held on streaming v1.4.0") {
		t.Fatalf("log:\n%s", log.String())
	}
}

// A held release whose binary is nowhere on this machine: said loudly (an error line and
// an event), and the slots run this release's binary.
func TestHeldTVBinaryMissingFallsBackLoudly(t *testing.T) {
	old := version.Version
	version.Version = "v1.5.0"
	t.Cleanup(func() { version.Version = old })

	green := heldSlot(t, "green", func(context.Context) (string, error) { return "v1.4.0", nil })
	base := green.Cmd.Path
	log := &syncWriter{w: &bytes.Buffer{}}
	s := New([]Process{green}, testPolicy(), WithKeptStreaming(func(string) []string { return nil }),
		WithLog(log))
	running(t, s)
	snap := waitFor(t, s, "green running", slotRunning("green"))
	if got := runningPath(s, "green"); got != base {
		t.Fatalf("runs %s, want this release's %s", got, base)
	}
	if v, p := s.HeldStreaming(); v != "v1.4.0" || p != "" {
		t.Fatalf("HeldStreaming = %q %q", v, p)
	}
	if !strings.Contains(log.String(), "level=error the live TV is held on streaming v1.4.0") {
		t.Fatalf("not said loudly:\n%s", log.String())
	}
	found := false
	for _, e := range snap.Events {
		found = found || (e.Kind == EventTVHeldMissing && strings.Contains(e.Message, "v1.4.0"))
	}
	if !found {
		t.Fatalf("no %s event: %+v", EventTVHeldMissing, snap.Events)
	}
	if got := s.ReleasesInUse(); len(got) != 0 {
		t.Fatalf("in use %v", got)
	}
}

// Not held (the query says so, or cannot say): the slots run this release's binary.
func TestNotHeldTVSlotsRunTheCurrentBinary(t *testing.T) {
	for name, held := range map[string]func(context.Context) (string, error){
		"not held":   func(context.Context) (string, error) { return "", nil },
		"unreadable": func(context.Context) (string, error) { return "", errors.New("connection refused") },
	} {
		t.Run(name, func(t *testing.T) {
			green := heldSlot(t, "green", held)
			base := green.Cmd.Path
			s := New([]Process{green}, testPolicy(), WithLog(&syncWriter{w: &bytes.Buffer{}}),
				WithKeptStreaming(func(string) []string { t.Error("looked for a kept binary"); return nil }))
			running(t, s)
			waitFor(t, s, "green running", slotRunning("green"))
			if got := runningPath(s, "green"); got != base {
				t.Fatalf("runs %s, want %s", got, base)
			}
		})
	}
}

func TestStagingOff(t *testing.T) {
	for raw, off := range map[string]bool{"": false, "{}": false, `{"enabled": true}`: false,
		`{"enabled": false, "hot_standby": true}`: true, `{"enabled": "no"}`: false, "not json": false} {
		if got := stagingOff(raw); got != off {
			t.Errorf("stagingOff(%q) = %v", raw, got)
		}
	}
	for v, ok := range map[string]bool{"v0.3.37": true, "0.0.1": true, "dev": false, "": false, "0.3.39-2-gabc1234": true} {
		if isRelease(v) != ok {
			t.Errorf("isRelease(%q) = %v", v, !ok)
		}
	}
}

// syncWriter is a log the supervisor's goroutines write at once.
type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.String()
}
