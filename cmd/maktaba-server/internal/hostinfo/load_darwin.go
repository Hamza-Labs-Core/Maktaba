//go:build darwin

package hostinfo

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// LoadAverage is the one-minute load average (runnable processes, averaged), and false where
// the system does not keep one.
func LoadAverage() (float64, bool) {
	// struct loadavg { fixpt_t ldavg[3]; long fscale; }: three uint32, padding, an int64
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(b) < 24 {
		return 0, false
	}
	scale := binary.LittleEndian.Uint64(b[16:24])
	if scale == 0 {
		return 0, false
	}
	return float64(binary.LittleEndian.Uint32(b[0:4])) / float64(scale), true
}
