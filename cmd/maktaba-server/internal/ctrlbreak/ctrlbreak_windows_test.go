//go:build windows

package ctrlbreak

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// envHelper: TestHelperProcess, run as a child, acts out "<mode>:<file>":
// exit-when (writes <file>.ready, exits once <file> exists) or break (writes
// <file>.ready, writes <file> and exits on CTRL_BREAK).
const envHelper = "CTRLBREAK_TEST_HELPER"

func TestHelperProcess(t *testing.T) {
	mode, file, ok := strings.Cut(os.Getenv(envHelper), ":")
	if !ok {
		t.Skip("the helper the process tests start")
	}
	var interrupted chan os.Signal
	if mode == "break" {
		interrupted = make(chan os.Signal, 1)
		signal.Notify(interrupted, os.Interrupt) // CTRL_BREAK, in Go
	}
	if err := os.WriteFile(file+".ready", nil, 0o644); err != nil {
		os.Exit(2)
	}
	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-interrupted:
			_ = os.WriteFile(file, []byte("interrupted"), 0o644)
			os.Exit(0)
		case <-deadline:
			os.Exit(3)
		case <-time.After(10 * time.Millisecond):
			if _, err := os.Stat(file); err == nil && mode == "exit-when" {
				os.Exit(0)
			}
		}
	}
}

// helperCmd is TestHelperProcess in mode, not started, and the file it acts on.
func helperCmd(t *testing.T, ctx context.Context, mode string) (*exec.Cmd, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts processes: runs without -short (the launcher gate on Windows)")
	}
	file := filepath.Join(t.TempDir(), "file")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), envHelper+"="+mode+":"+file)
	return cmd, file
}

// started waits until the helper runs; the cleanup kills and reaps it.
func started(t *testing.T, cmd *exec.Cmd, file string) {
	t.Helper()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(file + ".ready"); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper did not start")
		}
	}
}

// helper starts TestHelperProcess in mode in a process group of its own and
// waits until it runs. It returns the child, its file, and a handle with Access.
func helper(t *testing.T, mode string) (*exec.Cmd, string, windows.Handle) {
	t.Helper()
	cmd, file := helperCmd(t, context.Background(), mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(Access, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(h) })
	started(t, cmd, file)
	return cmd, file, h
}

// From the check to the send the process is held, so it cannot exit in
// between; it runs on once asked.
func TestHoldKeepsTheProcessUntilAsked(t *testing.T) {
	_, file, h := helper(t, "exit-when")
	sent := 0
	err := Hold(h, func() error {
		sent++
		if err := os.WriteFile(file, nil, 0o644); err != nil { // told to exit while held
			return err
		}
		time.Sleep(300 * time.Millisecond)
		if exited(h) {
			t.Error("the process exited while held")
		}
		return nil
	})
	if err != nil || sent != 1 {
		t.Fatalf("a running process: Hold = %v, sent %d", err, sent)
	}
	if ev, err := windows.WaitForSingleObject(h, 20_000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("the process was not resumed after it was asked (%d, %v)", ev, err)
	}
}

// A process that has exited is not asked, and says so.
func TestHoldDoesNotAskAnExitedProcess(t *testing.T) {
	_, file, h := helper(t, "exit-when")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ev, err := windows.WaitForSingleObject(h, 20_000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("the helper did not exit (%d, %v)", ev, err)
	}
	err := Hold(h, func() error { t.Error("an exited process was asked"); return nil })
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Hold = %v, want os.ErrProcessDone", err)
	}
}

// NewStopper starts the child in a process group of its own (CTRL_BREAK names
// it alone) and sets its Cancel.
func TestNewStopperSetsTheGroup(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit")
	NewStopper(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("SysProcAttr = %+v: no process group of its own", cmd.SysProcAttr)
	}
	if cmd.Cancel == nil {
		t.Fatal("no Cancel")
	}
}

// A cancelled context asks a running child with CTRL_BREAK, and only it: this
// process is on the same console and is not interrupted.
func TestStopperAsksTheChildOnCancel(t *testing.T) {
	if !CanAsk() {
		t.Skip("no console to send CTRL_BREAK through")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, file := helperCmd(t, ctx, "break")
	s := NewStopper(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.Started()
	started(t, cmd, file)
	self := make(chan os.Signal, 1)
	signal.Notify(self, os.Interrupt)
	defer signal.Stop(self)
	cancel()
	_ = cmd.Wait() // the context's error: it was asked
	s.Close()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("the child exited %d, not on its CTRL_BREAK", code)
	}
	if b, _ := os.ReadFile(file); string(b) != "interrupted" {
		t.Fatalf("the child did not take CTRL_BREAK (file %q)", b)
	}
	select {
	case <-self:
		t.Fatal("the CTRL_BREAK reached this process too")
	case <-time.After(200 * time.Millisecond):
	}
}

// A Cancel before Started (the context ended as the child started) still asks
// it: Wait has not run, so its pid is still its own.
func TestStopperBeforeStarted(t *testing.T) {
	if !CanAsk() {
		t.Skip("no console to send CTRL_BREAK through")
	}
	cmd, file := helperCmd(t, context.Background(), "break")
	s := NewStopper(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	started(t, cmd, file)
	if err := s.cancel(); err != nil {
		t.Fatalf("cancel before Started: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the child, asked: %v", err)
	}
	if b, _ := os.ReadFile(file); string(b) != "interrupted" {
		t.Fatalf("the child did not take CTRL_BREAK (file %q)", b)
	}
}

// After Wait and Close nothing is asked and no pid is opened: the child is
// done.
func TestStopperAfterClose(t *testing.T) {
	cmd, file := helperCmd(t, context.Background(), "exit-when")
	s := NewStopper(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.Started()
	started(t, cmd, file)
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := s.cancel(); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("cancel after Close = %v, want os.ErrProcessDone", err)
	}
}
