//go:build !unix

package system

import "errors"

// reexecSelf is unavailable off unix (no syscall.Exec). On Windows the
// running binary has already been swapped (move-aside-then-replace) and
// the supervisor relaunches the process; this stub keeps the build green
// and signals the caller that an in-process re-exec didn't happen.
func reexecSelf(string) error {
	return errors.New("re-exec unsupported on this platform; relaunch handled by the supervisor")
}
