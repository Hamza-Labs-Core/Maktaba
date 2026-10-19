//go:build !windows

package procattr

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// alive reports whether pid is a live (not zombie) process.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndexByte(string(raw), ')'); i >= 0 && i+2 < len(raw) {
			return raw[i+2] != 'Z'
		}
	}
	return true
}
