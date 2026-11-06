//go:build logbench && unix

package logbench

import (
	"syscall"
	"time"
)

// cpuTime is the CPU this process has used (user and system): a cost "of a core" is read
// from it, not from the wall clock, which a busy runner stretches.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
