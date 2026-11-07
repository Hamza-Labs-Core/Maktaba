//go:build unix

package pluginflow

import (
	"syscall"
	"time"
)

// cpuTime is the user and system time this process has used.
func cpuTime() time.Duration {
	var u syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &u) != nil {
		return 0
	}
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}
