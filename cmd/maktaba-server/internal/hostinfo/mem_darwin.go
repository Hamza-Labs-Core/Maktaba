//go:build darwin

package hostinfo

import (
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// FreeMemory is the memory a new process can have without the machine paging, in bytes
// (0 when unknown): the kernel's memory status level (the share of RAM available, what
// Activity Monitor's memory pressure reads) of the RAM. Free pages alone leave out the
// inactive ones the kernel hands out at once.
func FreeMemory() uint64 {
	level, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil || level == 0 || level > 100 {
		return 0
	}
	return TotalMemory() / 100 * uint64(level)
}

// pidMax is the kernel's largest pid (PID_MAX).
const pidMax = 99999

// ProcessRSS is the resident memory of each running pid, in bytes (ps: macOS has no
// /proc, and the call that reads it needs cgo). A pid that is gone has no entry.
func ProcessRSS(pids []int) map[int]uint64 {
	out := map[int]uint64{}
	if len(pids) == 0 {
		return out
	}
	list := make([]string, 0, len(pids))
	for _, p := range pids {
		if p > 0 && p <= pidMax { // ps refuses the whole list for one out of range
			list = append(list, strconv.Itoa(p))
		}
	}
	if len(list) == 0 {
		return out
	}
	b, _ := exec.Command("/bin/ps", "-o", "pid=,rss=", "-p", strings.Join(list, ",")).Output()
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		kb, err2 := strconv.ParseUint(f[1], 10, 64)
		if err1 == nil && err2 == nil {
			out[pid] = kb << 10
		}
	}
	return out
}
