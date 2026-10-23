//go:build !windows

package dbsafe

import (
	"os"
	"strconv"
	"syscall"
)

// deviceOf names the disk a folder is on (its device number).
func deviceOf(path string, info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Dev), 10)
	}
	return path
}
