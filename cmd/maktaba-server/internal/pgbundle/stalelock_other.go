//go:build !windows && !darwin

package pgbundle

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processName is a running process's command name (alive false: no such process; ""
// with alive: one it cannot tell, such as on a system without /proc, left to Postgres).
func processName(pid int) (name string, alive bool) {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return "", false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return "", true
	}
	return strings.TrimSpace(string(b)), true
}
