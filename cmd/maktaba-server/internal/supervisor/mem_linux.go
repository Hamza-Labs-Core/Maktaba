//go:build linux

package supervisor

import "golang.org/x/sys/unix"

// totalMemory is the machine's RAM in bytes (0 when unknown).
var totalMemory = func() uint64 {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0
	}
	return uint64(si.Totalram) * uint64(si.Unit)
}
