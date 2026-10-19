//go:build linux

package procattr

import (
	"os/exec"
	"syscall"
)

// Tie makes the kernel SIGKILL cmd's process when this one exits. Call
// it before Start.
func Tie(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

// ContainSelf is a no-op on Linux; Tie covers each child.
func ContainSelf() error { return nil }

// Contained reports whether children are tied to this process (always,
// through Tie, on Linux).
func Contained() bool { return true }
