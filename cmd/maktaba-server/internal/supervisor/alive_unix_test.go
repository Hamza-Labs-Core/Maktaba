//go:build !windows

package supervisor

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processAlive reports whether pid is a live (not zombie) process.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// Linux: a killed orphan may linger as a zombie until init reaps it.
	if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndexByte(string(raw), ')'); i >= 0 && i+2 < len(raw) {
			return raw[i+2] != 'Z'
		}
	}
	return true
}
