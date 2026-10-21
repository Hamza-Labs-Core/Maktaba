//go:build windows

package ctrlbreak

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

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

// helper starts TestHelperProcess in mode in a process group of its own and
// waits until it runs. It returns the child, the file it acts on, and a handle
// with Access.
func helper(t *testing.T, mode string) (*exec.Cmd, string, windows.Handle) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts processes: runs without -short (the launcher gate on Windows)")
	}
	file := filepath.Join(t.TempDir(), "file")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), envHelper+"="+mode+":"+file)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(Access, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(h)
	})
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(file + ".ready"); err == nil {
			return cmd, file, h
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper did not start")
		}
	}
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
	cmd, file, h := helper(t, "exit-when")
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
	// Ask too: the child is not reaped yet (its pid is still its own)
	if err := Ask(cmd.Process.Pid); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Ask = %v, want os.ErrProcessDone", err)
	}
}

// Ask sends a running child CTRL_BREAK, and only it: this process is on the
// same console and is not interrupted.
func TestAskSendsCTRLBreakToTheChild(t *testing.T) {
	cmd, file, _ := helper(t, "break")
	if !hasConsole() {
		t.Skip("no console to send CTRL_BREAK through")
	}
	self := make(chan os.Signal, 1)
	signal.Notify(self, os.Interrupt)
	defer signal.Stop(self)
	if err := Ask(cmd.Process.Pid); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the child, asked: %v", err)
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

// hasConsole: this process has a console (a CTRL_BREAK goes through one).
func hasConsole() bool {
	var pids [4]uint32
	n, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList").
		Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n != 0
}
