//go:build unix

package system

import (
	"os"
	"syscall"
)

// reexecSelf replaces the current process image with a fresh exec of the
// (just-swapped) binary, preserving argv and environment so the restart
// is seamless (same PID, inherited listeners under a supervisor). Unix
// only; the Windows build relaunches via the supervisor.
func reexecSelf(path string) error {
	return syscall.Exec(path, os.Args, os.Environ()) //nolint:gosec // path is our own resolved executable
}
