//go:build windows

package logkeeper

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FreeBytes is the space free for this user on path's volume (-1: unknown).
func FreeBytes(path string) int64 {
	for p := path; ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			u, err := windows.UTF16PtrFromString(p)
			if err != nil {
				return -1
			}
			var free uint64
			if windows.GetDiskFreeSpaceEx(u, &free, nil, nil) != nil {
				return -1
			}
			return int64(free)
		}
		if filepath.Dir(p) == p {
			return -1
		}
	}
}
