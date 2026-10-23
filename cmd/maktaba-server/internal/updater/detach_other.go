//go:build !windows

package updater

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a session of its own (it outlives the server's process group).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
