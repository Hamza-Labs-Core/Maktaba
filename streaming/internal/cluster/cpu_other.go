//go:build !linux && !windows

package cluster

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// cpuTimes is not read on this OS (macOS has no /proc); loadFallback uses the load
// average instead.
func cpuTimes() (idle, total uint64, ok bool) { return 0, 0, false }

// loadFallback is the 1-minute load average per CPU (`sysctl -n vm.loadavg`: "{ 1.2 0.9 0.8 }").
func loadFallback() float64 {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0
	}
	f := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	u := v / float64(runtime.NumCPU())
	if u > 1 {
		u = 1
	}
	return u
}
