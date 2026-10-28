package supervisor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
)

// The owner's rule (#404, 2026-10-28): while an update's TV check failed, the live TV
// stays on its old streaming software until the update's Retry passes or a later
// release's own binary change passes its check. A schedule or settings change made
// meanwhile records the held version and its staging slot and swap run the held binary;
// a release that names no binary (one built before, a source checkout's 0.0.1) runs it
// too; and a release whose binary is nowhere is refused at once, with the reason.

func runningEnv(s *Supervisor, instance string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.proc.Instance == instance && c.cmd != nil && c.status.PID > 0 {
			return append([]string(nil), c.cmd.Env...)
		}
	}
	return nil
}

func TestHeldTVReleasesRunTheHeldBinary(t *testing.T) {
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
	held := func(context.Context) (string, error) { return "v1.4.0", nil }
	green, amber := heldSlot(t, "green", held), heldSlot(t, "amber", held)
	s := New([]Process{green, amber}, testPolicy(), WithKeptStreaming(finder), WithLog(&syncWriter{w: &bytes.Buffer{}}))
	// the live slot bound to the live release, which names no binary (the owner's live
	// release recorded the pipeline's 0.0.1; the front door names none for it): the held
	// binary, bound to its release, not 1.5.0
	if err := s.Bind("green", &SlotBinding{ReleaseID: "r-live"}); err != nil {
		t.Fatal(err)
	}
	// a schedule change during the hold records the held version: its staging slot runs
	// the kept 1.4.0 (as its swap binds the live slot to it)
	if err := s.Bind("amber", &SlotBinding{ReleaseID: "r-sched", Version: "v1.4.0"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Want(RoleTVSlot, "amber", true); err != nil {
		t.Fatal(err)
	}
	running(t, s)
	waitFor(t, s, "green running", slotRunning("green"))
	waitFor(t, s, "amber running", slotRunning("amber"))
	if got := runningPath(s, "green"); got != kept {
		t.Fatalf("the live slot on a release naming no binary runs %s, want the held %s", got, kept)
	}
	if env := runningEnv(s, "green"); !slices.Contains(env, "MAKTABA_TV_RELEASE=r-live") {
		t.Fatalf("the live slot lost its release: %v", env)
	}
	if got := runningPath(s, "amber"); got != kept {
		t.Fatalf("the schedule change's staging slot runs %s, want the held %s", got, kept)
	}
}

// Not held: a release naming no binary runs this release's binary, bound to its release.
func TestUnversionedReleaseRunsTheCurrentBinaryWhenNotHeld(t *testing.T) {
	green := heldSlot(t, "green", func(context.Context) (string, error) { return "", nil })
	base := green.Cmd.Path
	s := New([]Process{green}, testPolicy(), WithLog(&syncWriter{w: &bytes.Buffer{}}))
	if err := s.Bind("green", &SlotBinding{ReleaseID: "r-live"}); err != nil {
		t.Fatal(err)
	}
	running(t, s)
	waitFor(t, s, "green running", slotRunning("green"))
	if got := runningPath(s, "green"); got != base {
		t.Fatalf("runs %s, want %s", got, base)
	}
	if env := runningEnv(s, "green"); !slices.Contains(env, "MAKTABA_TV_RELEASE=r-live") {
		t.Fatalf("env %v", env)
	}
}

// The held release's binary is gone: the front door's request to stage on it is refused
// at once with the reason (409), said loudly, and the slot is not bound to it.
func TestStagingOnAMissingHeldBinaryIsRefused(t *testing.T) {
	old := version.Version
	version.Version = "v1.5.0"
	t.Cleanup(func() { version.Version = old })

	amber := heldSlot(t, "amber", func(context.Context) (string, error) { return "v1.4.0", nil })
	log := &syncWriter{w: &bytes.Buffer{}}
	s := New([]Process{amber}, testPolicy(), WithKeptStreaming(func(string) []string { return nil }), WithLog(log))
	running(t, s)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/slots/amber/start", "application/json",
		strings.NewReader(`{"release_id":"r-sched","version":"v1.4.0","sha256":""}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "streaming v1.4.0") ||
		!strings.Contains(string(body), "not on this machine") {
		t.Fatalf("start on a missing binary: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(log.String(), "level=error tv-slot/amber cannot run TV release r-sched") {
		t.Fatalf("not said loudly:\n%s", log.String())
	}
	s.mu.Lock()
	b := s.children[0].binding
	s.mu.Unlock()
	if b != nil {
		t.Fatalf("amber was bound: %+v", b)
	}
}
