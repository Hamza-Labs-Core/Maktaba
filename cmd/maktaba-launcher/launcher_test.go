package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The test binary doubles as the fake maktaba-server the launcher starts (it is
// copied into bin/releases/<v>/), as the launcher's own main, and as a process
// that switches releases in a loop for the power-loss test.
const (
	envMain     = "LAUNCHER_TEST_MAIN"
	envSwitcher = "LAUNCHER_TEST_SWITCHER"
)

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(envMain) == "1":
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	case os.Getenv(envSwitcher) != "":
		switcherMain(os.Getenv(envSwitcher))
		return
	case isFakeChild():
		fakeServerMain()
		return
	}
	os.Exit(m.Run())
}

// isFakeChild: started by a launcher as a release's maktaba-server (the
// release directory has a behavior script; a Windows service's environment
// does not carry the test's variables).
func isFakeChild() bool {
	rd := os.Getenv(layout.EnvReleaseDir)
	if rd == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(rd, "behavior"))
	return err == nil
}

// fakeServerMain acts out <release>/behavior: comma-separated actions, one per
// start (the last repeats): exit:N, crash, run (until asked to stop),
// confirm-run (remove pending, then run), hang (ignore stop requests).
func fakeServerMain() {
	rel := os.Getenv(layout.EnvReleaseDir)
	root := os.Getenv(layout.EnvInstallRoot)
	b, _ := os.ReadFile(filepath.Join(rel, "behavior"))
	acts := strings.Split(strings.TrimSpace(string(b)), ",")
	n := bumpCounter(filepath.Join(rel, "starts"))
	act := acts[len(acts)-1]
	if n-1 < len(acts) {
		act = acts[n-1]
	}
	ver := filepath.Base(rel)
	appendLine(filepath.Join(root, "trace"), fmt.Sprintf("%d %s %s", time.Now().UnixMilli(), ver, act))
	switch {
	case strings.HasPrefix(act, "exit:"):
		code, _ := strconv.Atoi(strings.TrimPrefix(act, "exit:"))
		os.Exit(code)
	case strings.HasPrefix(act, "flip75:"):
		_ = layout.New(root).WritePointer(layout.CurrentFile, strings.TrimPrefix(act, "flip75:"))
		os.Exit(ExitRestart)
	case act == "crash":
		os.Exit(2)
	case act == "hang":
		// catch and drop (signal.Ignore would let Windows' default CTRL_BREAK handler end it)
		signal.Notify(make(chan os.Signal, 1), os.Interrupt, syscall.SIGTERM)
		time.Sleep(time.Hour)
	case act == "confirm-run":
		_ = layout.New(root).Confirm()
		fallthrough
	default: // run
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		stop := os.Getenv(EnvStopFile)
		for {
			select {
			case <-sig:
				appendLine(filepath.Join(root, "trace"), fmt.Sprintf("%d %s stopped", time.Now().UnixMilli(), ver))
				os.Exit(0)
			case <-time.After(10 * time.Millisecond):
				if _, err := os.Stat(stop); err == nil {
					appendLine(filepath.Join(root, "trace"), fmt.Sprintf("%d %s stopped", time.Now().UnixMilli(), ver))
					os.Exit(0)
				}
			}
		}
	}
}

func bumpCounter(path string) int {
	b, _ := os.ReadFile(path)
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	n++
	_ = os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644)
	return n
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// switcherMain flips root between two releases forever (killed by the test).
func switcherMain(root string) {
	l := layout.New(root)
	for i := 0; ; i++ {
		if i%2 == 0 {
			_ = l.Switch("2.0.0", "run-"+strconv.Itoa(i), time.Now())
		} else {
			_, _ = l.Rollback()
		}
	}
}

// --- helpers -----------------------------------------------------------------

func selfBinary(t *testing.T) string {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// mkRelease installs the test binary as bin/releases/<v>/maktaba-server with
// .verified and a behavior script.
func mkRelease(t *testing.T, l layout.Layout, v, behavior string) {
	t.Helper()
	dir := l.ReleaseDir(v)
	if err := layout.CopyFile(selfBinary(t), filepath.Join(dir, layout.Exe("maktaba-server"))); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.WriteVerified(dir, "test", v, time.Now()); err != nil {
		t.Fatal(err)
	}
	setBehavior(t, l, v, behavior)
}

func setBehavior(t *testing.T, l layout.Layout, v, behavior string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.ReleaseDir(v), "behavior"), []byte(behavior), 0o644); err != nil {
		t.Fatal(err)
	}
}

type traceLine struct {
	at  time.Time
	ver string
	act string
}

func readTrace(l layout.Layout) []traceLine {
	b, _ := os.ReadFile(filepath.Join(l.Root, "trace"))
	var out []traceLine
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(ln)
		if len(f) != 3 {
			continue
		}
		ms, _ := strconv.ParseInt(f[0], 10, 64)
		out = append(out, traceLine{at: time.UnixMilli(ms), ver: f[1], act: f[2]})
	}
	return out
}

func testConfig(root string) Config {
	return Config{
		Root:           root,
		PendingTimeout: 10 * time.Second,
		LoopWindow:     time.Minute,
		LoopMax:        3,
		BackoffInitial: 20 * time.Millisecond,
		BackoffMax:     80 * time.Millisecond,
		StableAfter:    time.Minute,
		StopGrace:      5 * time.Second,
		Poll:           20 * time.Millisecond,
		Stdout:         io.Discard,
		Stderr:         io.Discard,
	}
}

// startLauncher runs Run in the background; stop() cancels and waits.
func startLauncher(t *testing.T, cfg Config) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	var once sync.Once
	var err error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case err = <-done:
			case <-time.After(20 * time.Second):
				err = errors.New("launcher did not stop")
			}
		})
		return err
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func countAct(tr []traceLine, ver, act string) int {
	n := 0
	for _, l := range tr {
		if l.ver == ver && (act == "" || l.act == act) {
			n++
		}
	}
	return n
}

func events(l layout.Layout, name string) []Entry {
	var out []Entry
	for _, e := range readLog(l.LauncherLog()) {
		if e.Event == name {
			out = append(out, e)
		}
	}
	return out
}

func installed(t *testing.T, versions map[string]string, current string) layout.Layout {
	t.Helper()
	l := layout.New(t.TempDir())
	for v, b := range versions {
		mkRelease(t, l, v, b)
	}
	if current != "" {
		if err := l.Switch(current, "", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := l.Confirm(); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// --- tests -------------------------------------------------------------------

func TestLauncherRestartsOn75(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "exit:75,exit:75,run"}, "1.0.0")
	cfg := testConfig(l.Root)
	cfg.BackoffInitial = 2 * time.Second // a 75 must not wait for the backoff
	stop := startLauncher(t, cfg)
	waitFor(t, "third start", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	tr := readTrace(l)
	if d := tr[2].at.Sub(tr[0].at); d > 1500*time.Millisecond {
		t.Fatalf("restarts on 75 took %v (backoff applied?)", d)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if n := len(events(l, "launcher_restart")); n != 2 {
		t.Fatalf("launcher_restart events = %d, want 2", n)
	}
}

func TestLauncherRestart75ReReadsCurrent(t *testing.T) {
	t.Parallel()
	// what the supervisor does on a self-update (design §8): flip current, exit 75
	l := installed(t, map[string]string{"1.0.0": "flip75:2.0.0,run", "2.0.0": "confirm-run"}, "1.0.0")
	cfg := testConfig(l.Root)
	cfg.BackoffInitial = 2 * time.Second
	startLauncher(t, cfg)
	waitFor(t, "2.0.0 running", func() bool { return countAct(readTrace(l), "2.0.0", "confirm-run") == 1 })
	if n := countAct(readTrace(l), "1.0.0", ""); n != 1 {
		t.Fatalf("1.0.0 started %d times", n)
	}
}

func TestLauncherBackoff(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "crash"}, "1.0.0")
	cfg := testConfig(l.Root)
	cfg.BackoffInitial = 100 * time.Millisecond
	cfg.BackoffMax = 400 * time.Millisecond
	stop := startLauncher(t, cfg)
	waitFor(t, "six starts", func() bool { return countAct(readTrace(l), "1.0.0", "crash") >= 6 })
	_ = stop()
	tr := readTrace(l)
	var gaps []time.Duration
	for i := 1; i < 6; i++ {
		gaps = append(gaps, tr[i].at.Sub(tr[i-1].at))
	}
	// 100, 200, 400, 400, 400 ms of backoff (plus process start time)
	want := []time.Duration{100, 200, 400, 400, 400}
	for i, g := range gaps {
		w := want[i] * time.Millisecond
		if g < w-20*time.Millisecond || g > w+1500*time.Millisecond {
			t.Fatalf("gap %d = %v, want about %v (all gaps %v)", i, g, w, gaps)
		}
	}
	if gaps[4] > 2*gaps[2]+500*time.Millisecond {
		t.Fatalf("backoff not capped: %v", gaps)
	}
}

func TestLauncherStopExits0(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run"}, "1.0.0")
	stop := startLauncher(t, testConfig(l.Root))
	waitFor(t, "running", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	if err := stop(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if countAct(readTrace(l), "1.0.0", "stopped") != 1 {
		t.Fatal("the server was not asked to stop (it was killed)")
	}
	ev := events(l, "launcher_stopped")
	if len(ev) != 1 {
		t.Fatalf("launcher_stopped = %v", ev)
	}
	if code, _ := ev[0].Detail["exit"].(float64); code != 0 {
		t.Fatalf("server exit = %v", ev[0].Detail["exit"])
	}
}

func TestLauncherKillsAfterStopGrace(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "hang"}, "1.0.0")
	cfg := testConfig(l.Root)
	cfg.StopGrace = 300 * time.Millisecond
	stop := startLauncher(t, cfg)
	waitFor(t, "running", func() bool { return countAct(readTrace(l), "1.0.0", "hang") == 1 })
	time.Sleep(100 * time.Millisecond) // let it install its signal handlers
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if len(events(l, "launcher_kill")) != 1 {
		t.Fatal("a server that ignores the stop request was not killed")
	}
}

func TestBootLoopGuardFallsBack(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "crash"}, "1.0.0")
	if err := l.Switch("2.0.0", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	startLauncher(t, testConfig(l.Root))
	waitFor(t, "previous started", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	if n := countAct(readTrace(l), "2.0.0", "crash"); n != 3 {
		t.Fatalf("2.0.0 started %d times, want 3", n)
	}
	if cur, _ := l.Current(); cur != "1.0.0" {
		t.Fatalf("current = %q", cur)
	}
	if p, _ := l.ReadPending(); p != nil {
		t.Fatalf("pending left: %+v", p)
	}
	rb := events(l, "launcher_rollback")
	if len(rb) != 1 || rb[0].Version != "2.0.0" || rb[0].Detail["reason"] != "boot_loop" || rb[0].Detail["to"] != "1.0.0" {
		t.Fatalf("launcher_rollback = %+v", rb)
	}
}

func TestPendingTimeoutFallsBack(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
	if err := l.Switch("2.0.0", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(l.Root)
	cfg.PendingTimeout = 400 * time.Millisecond
	startLauncher(t, cfg)
	waitFor(t, "previous started", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	if countAct(readTrace(l), "2.0.0", "stopped") != 1 {
		t.Fatal("the unconfirmed release was not stopped before the fallback")
	}
	rb := events(l, "launcher_rollback")
	if len(rb) != 1 || rb[0].Detail["reason"] != "pending_timeout" {
		t.Fatalf("launcher_rollback = %+v", rb)
	}
	if cur, _ := l.Current(); cur != "1.0.0" {
		t.Fatalf("current = %q", cur)
	}
}

func TestConfirmedPendingStays(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "crash,confirm-run"}, "1.0.0")
	if err := l.Switch("2.0.0", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(l.Root)
	cfg.PendingTimeout = 400 * time.Millisecond
	startLauncher(t, cfg)
	waitFor(t, "confirmed", func() bool { return countAct(readTrace(l), "2.0.0", "confirm-run") == 1 })
	time.Sleep(800 * time.Millisecond) // past the pending timeout
	if cur, _ := l.Current(); cur != "2.0.0" {
		t.Fatalf("current = %q", cur)
	}
	if len(events(l, "launcher_rollback")) != 0 || countAct(readTrace(l), "1.0.0", "") != 0 {
		t.Fatal("a confirmed release was rolled back")
	}
}

func TestLauncherRefusesTamperedCurrent(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
	_ = l.Switch("2.0.0", "", time.Now())
	_ = l.Confirm()
	f, err := os.OpenFile(l.ServerPath("2.0.0"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("tampered")
	_ = f.Close()
	startLauncher(t, testConfig(l.Root))
	waitFor(t, "previous started", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
	ref := events(l, "launcher_refused")
	if len(ref) == 0 || ref[0].Version != "2.0.0" || !strings.Contains(fmt.Sprint(ref[0].Detail["reason"]), "digest") {
		t.Fatalf("launcher_refused = %+v", ref)
	}
	if countAct(readTrace(l), "2.0.0", "") != 0 {
		t.Fatal("the tampered release ran")
	}
}

func TestLauncherPrunedCurrentStartsPrevious(t *testing.T) { // EC2
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
	_ = l.Switch("2.0.0", "", time.Now())
	_ = l.Confirm()
	if err := os.RemoveAll(l.ReleaseDir("2.0.0")); err != nil {
		t.Fatal(err)
	}
	startLauncher(t, testConfig(l.Root))
	waitFor(t, "previous started", func() bool { return countAct(readTrace(l), "1.0.0", "run") == 1 })
}

func TestNoPreviousKeepsRetrying(t *testing.T) { // EC1
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "crash"}, "")
	if err := l.Switch("1.0.0", "first", time.Now()); err != nil { // first install: pending, no previous
		t.Fatal(err)
	}
	startLauncher(t, testConfig(l.Root))
	waitFor(t, "retries", func() bool { return countAct(readTrace(l), "1.0.0", "crash") >= 5 })
	if len(events(l, "no_previous")) == 0 {
		t.Fatal("no_previous not logged")
	}
	if cur, _ := l.Current(); cur != "1.0.0" {
		t.Fatalf("current = %q", cur)
	}
}

func TestSecondLauncherExits(t *testing.T) { // EC5
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run"}, "1.0.0")
	lk, err := l.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer layout.Unlock(lk)
	var stderr bytes.Buffer
	cfg := testConfig(l.Root)
	cfg.Stderr = &stderr
	if err := Run(context.Background(), cfg); !errors.Is(err, layout.ErrLocked) {
		t.Fatalf("Run = %v", err)
	}
	if !strings.Contains(stderr.String(), "already_running") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestPowerLossMidSwitch kills (SIGKILL / TerminateProcess) a process that is
// switching releases back and forth, at random moments, and then lets the
// launcher pick: every time it must start a release that validates, under
// the guard when it is the unconfirmed one.
func TestPowerLossMidSwitch(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "run"}, "1.0.0")
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < 25; i++ {
		cmd := exec.Command(selfBinary(t))
		cmd.Env = append(os.Environ(), envSwitcher+"="+l.Root)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(20+rng.Intn(60)) * time.Millisecond)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()

		ln := &launcher{cfg: testConfig(l.Root), l: l, log: newEventLog(l.LauncherLog(), io.Discard, time.Now)}
		if _, err := l.Reconcile(); err != nil {
			t.Fatal(err)
		}
		ver := ln.choose()
		ln.log.Close()
		if ver != "1.0.0" && ver != "2.0.0" {
			t.Fatalf("kill %d: launcher chose %q", i, ver)
		}
		if err := l.ValidateRelease(ver); err != nil {
			t.Fatalf("kill %d: %v", i, err)
		}
		cur, err := l.Current()
		if err != nil || cur != ver {
			t.Fatalf("kill %d: current %q (%v), chose %q", i, cur, err, ver)
		}
		p, err := l.ReadPending()
		if err != nil {
			t.Fatalf("kill %d: pending unreadable after reconcile: %v", i, err)
		}
		if p != nil && p.Version != cur {
			t.Fatalf("kill %d: pending %q with current %q", i, p.Version, cur)
		}
		if cur == "2.0.0" && p == nil {
			// a Rollback cut between its two steps cannot leave 2.0.0 current;
			// a Switch cut can only leave it current with its pending marker
			t.Fatalf("kill %d: 2.0.0 current without the guard", i)
		}
		// leave a clean, confirmed 1.0.0 for the next round
		_ = l.WritePointer(layout.CurrentFile, "1.0.0")
		_ = l.ClearPending()
	}
}

func TestStatusAndVerifyCLI(t *testing.T) {
	t.Parallel()
	l := installed(t, map[string]string{"1.0.0": "run", "2.0.0": "crash"}, "1.0.0")
	_ = l.RaiseWatermark("1.0.0")
	_ = l.Switch("2.0.0", "run-9", time.Now())
	lg := newEventLog(l.LauncherLog(), io.Discard, time.Now)
	lg.Event("launcher_rollback", "2.0.0", map[string]any{"reason": "boot_loop", "to": "1.0.0"})
	lg.Close()

	var out, errb bytes.Buffer
	if code := run([]string{"--root", l.Root, "--status"}, &out, &errb); code != 0 {
		t.Fatalf("status exit %d: %s", code, errb.String())
	}
	s := out.String()
	for _, want := range []string{"current:   2.0.0", "previous:  1.0.0", "pending:   2.0.0", "run run-9", "watermark: 1.0.0", "last rollback:", "boot_loop"} {
		if !strings.Contains(s, want) {
			t.Errorf("status lacks %q:\n%s", want, s)
		}
	}
	out.Reset()
	if code := run([]string{"--root", l.Root, "--status", "--json"}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"launcher_protocol": 1`) {
		t.Fatalf("status --json: %d %s", code, out.String())
	}
	out.Reset()
	if code := run([]string{"--verify", l.ReleaseDir("1.0.0")}, &out, &errb); code != 0 {
		t.Fatalf("verify exit %d: %s", code, errb.String())
	}
	_ = os.WriteFile(filepath.Join(l.ReleaseDir("1.0.0"), layout.Exe("maktaba-api")), []byte("unlisted"), 0o755)
	if code := run([]string{"--verify", l.ReleaseDir("1.0.0")}, &out, &errb); code != 0 {
		t.Fatal("verify failed on a file .verified does not list")
	}
	f, _ := os.OpenFile(l.ServerPath("1.0.0"), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("changed")
	_ = f.Close()
	if code := run([]string{"--verify", l.ReleaseDir("1.0.0")}, &out, &errb); code == 0 {
		t.Fatal("verify passed a changed file")
	}
	out.Reset()
	if code := run([]string{"--version"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "launcher_protocol 1") {
		t.Fatalf("version: %q", out.String())
	}
	if code := run([]string{"--root", t.TempDir(), "serve"}, &out, &errb); code != exitFailure ||
		!strings.Contains(errb.String(), "setup --self-managed") {
		t.Fatalf("serve outside a layout: %d %q", code, errb.String())
	}
}

func TestReplaceSelf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, layout.Exe("maktaba-launcher"))
	if err := os.WriteFile(target, []byte("old launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(dir, layout.Exe("maktaba-launcher.new"))
	if err := layout.CopyFile(selfBinary(t), newer); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(newer, "--replace-self", target)
	cmd.Env = append(os.Environ(), envMain+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("replace-self: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(target + ".old"); string(b) != "old launcher" {
		t.Fatalf(".old = %q", b)
	}
	if _, err := os.Stat(newer); !os.IsNotExist(err) {
		t.Fatal("the new launcher was copied, not moved into place")
	}
	if a, _ := layout.SHA256File(target); a != mustSHA(t, selfBinary(t)) {
		t.Fatal("target is not the new launcher")
	}
	// the next start removes .old
	l := layout.New(dir)
	removeOldSelf(l)
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Fatal(".old left after start")
	}
}

func mustSHA(t *testing.T, p string) string {
	t.Helper()
	s, err := layout.SHA256File(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
