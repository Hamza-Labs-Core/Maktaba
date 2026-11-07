//go:build linux

package hostinfo

import "golang.org/x/sys/unix"

// LoadAverage is the one-minute load average (runnable processes, averaged), and false where
// the system does not keep one.
func LoadAverage() (float64, bool) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, false
	}
	return float64(si.Loads[0]) / 65536, true // SI_LOAD_SHIFT
}
