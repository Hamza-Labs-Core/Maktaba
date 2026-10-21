//go:build !windows

package ctrlbreak

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A cancelled context sends the child SIGTERM.
func TestStopperSendsSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a process: runs without -short")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sleep", "30")
	s := NewStopper(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.Started()
	start := time.Now()
	cancel()
	_ = cmd.Wait()
	s.Close()
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("the child ended %v, not on SIGTERM", cmd.ProcessState)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("stopped after %s", d)
	}
}
