//go:build unix

package ffmpeg

import (
	"os"
	"syscall"
)

// nodeGroup is a render node's group id (-1: unknown).
func nodeGroup(path string) int {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Gid)
	}
	return -1
}
