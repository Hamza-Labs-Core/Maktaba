//go:build windows

package supervisor

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// rerunEnv names the test a rerun is for (rerunIn).
const rerunEnv = "MAKTABA_TEST_RERUN"

// rerunIn runs the calling test again, alone, in a process created with flags
// and reports false: the outer test passes when that run does. Inside the run
// it reports true and the test goes on.
func rerunIn(t *testing.T, flags uint32) bool {
	t.Helper()
	if testing.Short() {
		t.Skip("spawns child processes; runs without -short")
	}
	if os.Getenv(rerunEnv) == t.Name() {
		return true
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v")
	cmd.Env = append(os.Environ(), rerunEnv+"="+t.Name())
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+t.Name()) {
		t.Fatalf("the run in a process of its own: %v\n%s", err, out)
	}
	return false
}

// ownConsole runs the test in a console of its own with no window
// (CREATE_NO_WINDOW), which the supervisor and its children share as they do
// under the app's agent: the CTRL_BREAK tests pass whether or not `go test` has
// a console (an IDE, a service), open no window, and never send an event on
// the console a developer types in.
func ownConsole(t *testing.T) bool { return rerunIn(t, windows.CREATE_NO_WINDOW) }

// noConsole runs the test with no console at all (DETACHED_PROCESS), as a bare
// serve started detached by a GUI program.
func noConsole(t *testing.T) bool { return rerunIn(t, windows.DETACHED_PROCESS) }

// Every child starts suspended (attachTree resumes it in its job) and in a
// process group of its own (CTRL_BREAK reaches it alone); the command's own
// flags stay. Without a console to share, each also gets no window; with one,
// never (CREATE_NO_WINDOW would give it a console of its own, out of the
// CTRL_BREAK's reach).
func TestPrepareTreeStartsAProcessGroup(t *testing.T) {
	prev := hasConsole
	t.Cleanup(func() { hasConsole = prev })
	for _, console := range []bool{true, false} {
		hasConsole = func() bool { return console }
		want := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP)
		if !console {
			want |= windows.CREATE_NO_WINDOW
		}
		bare := &exec.Cmd{Path: "x"}
		prepareTree(bare)
		if got := bare.SysProcAttr.CreationFlags; got != want {
			t.Errorf("console %v: flags = %#x, want %#x", console, got, want)
		}
		own := &exec.Cmd{Path: "x"}
		own.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_UNICODE_ENVIRONMENT}
		prepareTree(own)
		if got := own.SysProcAttr.CreationFlags; got != want|windows.CREATE_UNICODE_ENVIRONMENT {
			t.Errorf("console %v: flags = %#x, want %#x", console, got, want|windows.CREATE_UNICODE_ENVIRONMENT)
		}
	}
}

// Without a console no child can be asked with CTRL_BREAK: each is killed at
// once instead (at shutdown, not after the grace; on request, not after the
// 10 s), and the reason is logged once.
func TestWithoutAConsoleChildrenAreKilled(t *testing.T) {
	if !noConsole(t) {
		return
	}
	if _, err := windows.GetConsoleCP(); err == nil {
		t.Fatal("this run has a console")
	}
	// A child of a process with no console gets a console of its own: the
	// supervisor gives it no window, so none opens on the desktop.
	window := map[Role]string{}
	for _, role := range []Role{RoleAPI, RoleStreaming, RoleTVSlot} {
		window[role] = filepath.Join(t.TempDir(), "window")
	}
	api, apiAsked := askedStop(t, RoleAPI, "exit", "FAKE_WINDOW_FILE="+window[RoleAPI])
	streaming, streamingAsked := askedStop(t, RoleStreaming, "exit", "FAKE_WINDOW_FILE="+window[RoleStreaming])
	slot, slotAsked := askedStop(t, RoleTVSlot, "exit", "FAKE_WINDOW_FILE="+window[RoleTVSlot])
	slot.Instance, slot.OnDemand = "amber", true
	logs := &syncBuffer{}
	s := New([]Process{api, streaming, slot}, testPolicy(), WithLog(logs))
	const grace = 20 * time.Second
	stop, _ := runningGrace(t, s, grace)
	if err := s.Want(RoleTVSlot, "amber", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "all running", func(snap Snapshot) bool {
		return runningPID(RoleAPI)(snap) && runningPID(RoleStreaming)(snap) && runningPID(RoleTVSlot)(snap)
	})

	start := time.Now()
	if err := s.Want(RoleTVSlot, "amber", false); err != nil {
		t.Fatal(err)
	}
	snap := waitFor(t, s, "the slot stopped", func(snap Snapshot) bool {
		c := proc(snap, RoleTVSlot)
		return c.State == StateStopped && c.PID == 0 && c.LastExit != nil
	})
	if took := time.Since(start); took >= 10*time.Second {
		t.Fatalf("stopping the slot took %s: it was not killed at once", took)
	}
	if exit := proc(snap, RoleTVSlot).LastExit; exit.Code != 1 || exit.Reason != ReasonStopped {
		t.Fatalf("slot exit = %+v, want killed (code 1), reason stopped", exit)
	}

	start = time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= grace/2 {
		t.Fatalf("shutdown took %s: the children were not killed at once", took)
	}
	for _, role := range []Role{RoleAPI, RoleStreaming} {
		if exit := proc(s.Snapshot(), role).LastExit; exit == nil || exit.Code != 1 || exit.Reason != ReasonShutdown {
			t.Errorf("%s exit = %+v, want killed (code 1), reason shutdown", role, exit)
		}
	}
	for role, marker := range map[Role]string{RoleAPI: apiAsked, RoleStreaming: streamingAsked, RoleTVSlot: slotAsked} {
		if wasAsked(marker) {
			t.Errorf("%s was asked to stop, without a console", role)
		}
	}
	if n := strings.Count(logs.String(), "cannot be asked to stop: no console"); n != 1 {
		t.Errorf("no console logged %d times, want once:\n%s", n, logs)
	}
	for role, f := range window {
		if got, err := os.ReadFile(f); err != nil || string(got) != "false" {
			t.Errorf("%s: a console window on the desktop: %q (%v)", role, got, err)
		}
	}
}

// The tree sends the ask only while its child runs: never once it has been
// reaped (its pid may be another process's by then) or has exited.
func TestTreeAsksOnlyARunningChild(t *testing.T) {
	t.Parallel()
	sent := 0
	send := func() error { sent++; return nil }
	var none *procTree
	for _, tree := range []*procTree{none, {}} {
		if err := tree.ask(send); err != nil || sent != 0 {
			t.Fatalf("no child: ask = %v, sent %d", err, sent)
		}
	}
}

// A running child is held from the check to the send (it cannot exit in
// between, which would send the event astray) and runs on afterwards.
func TestTreeAsksARunningChildAndResumesIt(t *testing.T) {
	p, addr := fakeProc(t, RoleAPI, "serve")
	prepareTree(p.Cmd)
	if err := p.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tree, err := attachTree(p.Cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tree.kill(); _ = p.Cmd.Wait(); tree.release() })
	waitHealthy(t, "http://"+addr+"/healthz")
	sent := 0
	if err := tree.ask(func() error {
		sent++
		if tree.exited() {
			t.Error("the child exited while held")
		}
		return nil
	}); err != nil || sent != 1 {
		t.Fatalf("a running child: ask = %v, sent %d", err, sent)
	}
	waitHealthy(t, "http://"+addr+"/healthz") // resumed: it answers again
}

func waitHealthy(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if resp, err := client.Get(url); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer", url)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTreeDoesNotAskAnExitedChild(t *testing.T) {
	p, _ := fakeProc(t, RoleAPI, "exit0")
	prepareTree(p.Cmd)
	if err := p.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tree, err := attachTree(p.Cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.release()
	if err := p.Cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := tree.ask(func() error { t.Error("an exited child was asked"); return nil }); err != nil {
		t.Fatal(err)
	}
}
