//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execProcess replaces this process with path (the container command's given command).
func execProcess(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }

// ownerOf is path's owner uid (the container's write check names it when it is not the
// container's user).
func ownerOf(path string) (int, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
