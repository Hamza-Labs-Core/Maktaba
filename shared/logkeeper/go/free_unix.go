//go:build unix

package logkeeper

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// FreeBytes is the space free for this user on path's volume (-1: unknown).
func FreeBytes(path string) int64 {
	for p := path; ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			var st unix.Statfs_t
			if unix.Statfs(p, &st) != nil {
				return -1
			}
			return int64(st.Bavail) * int64(st.Bsize) //nolint:unconvert // Bsize is uint32 on darwin, int64 on linux
		}
		if filepath.Dir(p) == p {
			return -1
		}
	}
}
