//go:build !windows

package appvenv

import (
	"os/exec"
	"syscall"
)

// ownGroup: on POSIX the bootstrap stays in the server's process group (a
// Ctrl-C in the server's terminal reaches it too, which it takes as a stop).
func ownGroup(*exec.Cmd) {}

// askStop sends the bootstrap SIGTERM: it stops pip, says interrupted and
// exits 130.
func askStop(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }
