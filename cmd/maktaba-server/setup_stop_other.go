//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// stopGently makes the context's cancellation ask cmd to stop (SIGTERM: the
// bootstrap stops pip and records where it was), then the kill after
// stepStopWait.
func stopGently(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = stepStopWait
}
