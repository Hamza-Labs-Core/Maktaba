//go:build darwin

package supervisor

import "golang.org/x/sys/unix"

// totalMemory is the machine's RAM in bytes (0 when unknown).
var totalMemory = func() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
