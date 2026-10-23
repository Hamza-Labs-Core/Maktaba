//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The Windows app (Epic 38, story 38.2): the stop event against the fake
// server, and app-bundle mode found from the install's files. Every test uses
// a stop event of its own, never Local\Maktaba.Server.Stop, which a Maktaba
// this PC runs may be using (and may have set: a paused server).

// mkWinBundle lays out <tmp>\Maktaba as the Windows app installs it, with the
// test binary as server\bin\maktaba-server.exe and a behavior script; it
// returns the payload directory and a separate state root.
func mkWinBundle(t *testing.T, behavior string) (payload, root string) {
	t.Helper()
	payload = mkWinInstall(t, filepath.Join(t.TempDir(), "Maktaba"))
	bin := filepath.Join(payload, "bin")
	if err := layout.CopyFile(selfBinary(t), filepath.Join(bin, "maktaba-server.exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "behavior"), []byte(behavior), 0o644); err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	if err := os.MkdirAll(layout.New(root).UpdatesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return payload, root
}

func testStopEventName() string {
	return fmt.Sprintf(`Local\Maktaba.Server.Stop.test-%d-%s`, os.Getpid(), randHex())
}

// testStopEvent creates a manual-reset stop event of the test's own, as the
// agent does before it starts the launcher.
func testStopEvent(t *testing.T) (string, windows.Handle) {
	t.Helper()
	name := testStopEventName()
	ev, err := windows.CreateEvent(nil, 1, 0, utf16Name(t, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(ev) })
	return name, ev
}

func openStopEvent(t *testing.T, name string) (windows.Handle, error) {
	return windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.SYNCHRONIZE, false, utf16Name(t, name))
}

func utf16Name(t *testing.T, name string) *uint16 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func isSet(ev windows.Handle) bool {
	s, _ := windows.WaitForSingleObject(ev, 0)
	return s == windows.WAIT_OBJECT_0
}

// bundleRun is Run in the background; the test ends it with the stop event,
// the cleanup (and a test that wants the CTRL_C path) with cancel.
type bundleRun struct {
	finished chan struct{}
	err      error
	cancel   context.CancelFunc
}

func startBundleRun(t *testing.T, cfg Config) *bundleRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &bundleRun{finished: make(chan struct{}), cancel: cancel}
	go func() {
		r.err = Run(ctx, cfg)
		close(r.finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.finished:
		case <-time.After(20 * time.Second):
		}
	})
	return r
}

// wait returns Run's error, failing the test when Run is still running after d.
func (r *bundleRun) wait(t *testing.T, d time.Duration) error {
	t.Helper()
	select {
	case <-r.finished:
		return r.err
	case <-time.After(d):
		t.Fatalf("Run still running after %v", d)
		return nil
	}
}

func bundleTestConfig(payload, root, stopEvent string) Config {
	cfg := testConfig(root)
	cfg.Bundle, cfg.StopEvent = payload, stopEvent
	cfg.StopGrace = 30 * time.Second // a kill would take this long
	return cfg
}

// checkAskedToStop: the fake server stopped on the stop file or CTRL_BREAK
// (it writes "stopped" only then), it was not killed, and it exited 0.
func checkAskedToStop(t *testing.T, l layout.Layout) {
	t.Helper()
	if countAct(readTrace(l), "bin", "stopped") != 1 {
		t.Fatalf("the server was not asked to stop (it was killed); trace %+v", readTrace(l))
	}
	if n := len(events(l, "launcher_kill")); n != 0 {
		t.Fatalf("launcher_kill = %d, want 0", n)
	}
	st := events(l, "launcher_stopped")
	if len(st) != 1 {
		t.Fatalf("launcher_stopped = %+v", st)
	}
	if code, _ := st[0].Detail["exit"].(float64); code != 0 {
		t.Fatalf("server exit = %v", st[0].Detail["exit"])
	}
}

func TestBundleStopEventStopsServer(t *testing.T) {
	t.Parallel()
	processTest(t)
	payload, root := mkWinBundle(t, "run")
	name, ev := testStopEvent(t)
	cfg := bundleTestConfig(payload, root, name)
	r := startBundleRun(t, cfg)
	l := layout.New(root)
	waitFor(t, "running", func() bool { return countAct(readTrace(l), "bin", "run") == 1 })
	time.Sleep(100 * time.Millisecond) // let it install its signal handlers

	asked := time.Now()
	if err := windows.SetEvent(ev); err != nil {
		t.Fatal(err)
	}
	if err := r.wait(t, 20*time.Second); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if d := time.Since(asked); d > 5*time.Second {
		t.Fatalf("Run returned %v after the stop event (stop grace %v: it waited for the kill)", d, cfg.StopGrace)
	}
	checkAskedToStop(t, l)
	if se := events(l, "launcher_stop_event"); len(se) != 1 || se[0].Detail["name"] != name {
		t.Fatalf("launcher_stop_event = %+v", se)
	}
	if !isSet(ev) {
		t.Fatal("the launcher reset the stop event (the agent owns that)")
	}
}

// A Pause before the launcher starts (the event set already): the server is
// never started.
func TestBundleStopEventSetBeforeStart(t *testing.T) {
	t.Parallel()
	processTest(t)
	payload, root := mkWinBundle(t, "run")
	name, ev := testStopEvent(t)
	if err := windows.SetEvent(ev); err != nil {
		t.Fatal(err)
	}
	r := startBundleRun(t, bundleTestConfig(payload, root, name))
	if err := r.wait(t, 10*time.Second); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	l := layout.New(root)
	if n := countAct(readTrace(l), "bin", ""); n != 0 {
		t.Fatalf("the server was started %d times under a set stop event", n)
	}
	if len(events(l, "launcher_stop_event")) != 1 {
		t.Fatalf("launcher.log: %+v", readLog(l.LauncherLog()))
	}
	if !isSet(ev) {
		t.Fatal("the launcher reset the stop event")
	}
}

// No agent made the event: the launcher creates it, manual-reset (its own
// wait must not clear it, the agent waits on it too), and closes it when the
// run ends.
func TestBundleStopEventCreatedByLauncher(t *testing.T) {
	t.Parallel()
	processTest(t)
	payload, root := mkWinBundle(t, "run")
	name := testStopEventName()
	r := startBundleRun(t, bundleTestConfig(payload, root, name))
	l := layout.New(root)
	waitFor(t, "running", func() bool { return countAct(readTrace(l), "bin", "run") == 1 })
	time.Sleep(100 * time.Millisecond)

	ev, err := openStopEvent(t, name)
	if err != nil {
		t.Fatalf("the launcher did not create %s: %v", name, err)
	}
	defer func() { _ = windows.CloseHandle(ev) }()
	if err := windows.SetEvent(ev); err != nil {
		t.Fatal(err)
	}
	if err := r.wait(t, 20*time.Second); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	checkAskedToStop(t, l)
	if !isSet(ev) {
		t.Fatal("the event the launcher created is not manual-reset (its wait cleared it)")
	}
}

// Stopped another way (CTRL_C, the service's stop): the same clean stop, and
// Run ends its waiter and closes the event on the way out.
func TestBundleStopEventReleasedWhenRunEnds(t *testing.T) {
	t.Parallel()
	processTest(t)
	payload, root := mkWinBundle(t, "run")
	name := testStopEventName()
	r := startBundleRun(t, bundleTestConfig(payload, root, name))
	l := layout.New(root)
	waitFor(t, "running", func() bool { return countAct(readTrace(l), "bin", "run") == 1 })
	time.Sleep(100 * time.Millisecond)

	r.cancel()
	if err := r.wait(t, 20*time.Second); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	checkAskedToStop(t, l)
	if len(events(l, "launcher_stop_event")) != 0 {
		t.Fatal("launcher_stop_event logged for a stop through the context")
	}
	// the launcher held the only handle: once it is closed the name is gone
	if ev, err := openStopEvent(t, name); !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		if err == nil {
			_ = windows.CloseHandle(ev)
		}
		t.Fatalf("open %s after Run: %v, want ERROR_FILE_NOT_FOUND (handle left open)", name, err)
	}
}

// TestWindowsAppServeStopEvent drives `maktaba-launcher --app-bundle
// <install>\server serve`, what the agent starts, through run(): app-bundle
// mode watches stopEventName. Not parallel: it points stopEventName at an
// event of its own while it runs.
func TestWindowsAppServeStopEvent(t *testing.T) {
	processTest(t)
	payload, root := mkWinBundle(t, "run")
	name, ev := testStopEvent(t)
	saved := stopEventName
	stopEventName = name
	t.Cleanup(func() { stopEventName = saved })

	var code int
	finished := make(chan struct{})
	go func() {
		code = run([]string{"--app-bundle", payload, "--root", root, "--stop-grace", "30s", "serve"}, io.Discard, io.Discard)
		close(finished)
	}()
	t.Cleanup(func() {
		_ = windows.SetEvent(ev)
		select {
		case <-finished:
		case <-time.After(20 * time.Second):
		}
	})
	l := layout.New(root)
	waitFor(t, "running", func() bool { return countAct(readTrace(l), "bin", "run") == 1 })
	time.Sleep(100 * time.Millisecond)

	asked := time.Now()
	if err := windows.SetEvent(ev); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not end after the stop event")
	}
	if code != exitOK {
		t.Fatalf("serve exit %d, want %d", code, exitOK)
	}
	if d := time.Since(asked); d > 5*time.Second {
		t.Fatalf("serve ended %v after the stop event (it waited for the kill)", d)
	}
	checkAskedToStop(t, l)
	b, _ := os.ReadFile(filepath.Join(root, "env"))
	if got := string(b); got != "update_disable= runtime_owner= launcher= release_dir=\n" {
		t.Fatalf("server env %q: want no release layout", got)
	}
}

// TestWindowsAppDetectedStatus runs the launcher from <install>\server\bin
// with no --app-bundle: the install's files put it in app-bundle mode, its
// state is %LOCALAPPDATA%\Maktaba, and --status says so with Windows paths.
// Without Maktaba.exe it is a self-managed launcher again.
func TestWindowsAppDetectedStatus(t *testing.T) {
	t.Parallel()
	processTest(t)
	install := filepath.Join(t.TempDir(), "Maktaba")
	payload := mkWinInstall(t, install)
	launcher := filepath.Join(payload, "bin", "maktaba-launcher.exe")
	if err := layout.CopyFile(selfBinary(t), launcher); err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	status := func() []byte {
		t.Helper()
		cmd := exec.Command(launcher, "--status", "--json")
		cmd.Env = append(os.Environ(), envMain+"=1", layout.EnvInstallRoot+"=", "LOCALAPPDATA="+local)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("--status: %v\n%s", err, out)
		}
		return out
	}

	out := status()
	var st BundleStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wantPayload, err := filepath.EvalSymlinks(payload) // os.Executable's long-name form (RUNNER~1 on CI)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(local, "Maktaba")
	if st.AppBundle != wantPayload || st.Server != filepath.Join(wantPayload, "bin", "maktaba-server.exe") ||
		st.Root != wantRoot || st.LauncherLog != filepath.Join(wantRoot, "updates", "launcher.log") ||
		st.StopEvent != StopEventName || !strings.HasPrefix(st.Updates, "the cluster's update: staged here") {
		t.Fatalf("--status --json = %+v\nwant app_bundle %q, root %q", st, wantPayload, wantRoot)
	}

	if err := os.Remove(filepath.Join(install, "Maktaba.exe")); err != nil {
		t.Fatal(err)
	}
	var rs map[string]any
	if out := status(); json.Unmarshal(out, &rs) != nil || rs["app_bundle"] != nil || rs["launcher_protocol"] == nil {
		t.Fatalf("without Maktaba.exe: want the self-managed status, got %s", out)
	}
}
