//go:build linux

package hostinfo

import (
	"os"
	"strconv"
	"strings"
)

// FreeMemory is MemAvailable of /proc/meminfo, in bytes (0 when unknown): what a new
// process can have without the machine swapping.
func FreeMemory() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				if kb, err := strconv.ParseUint(f[0], 10, 64); err == nil {
					return kb << 10
				}
			}
		}
	}
	return 0
}

// ProcessRSS is the resident memory of each running pid, in bytes (/proc/<pid>/statm). A
// pid that is gone has no entry.
func ProcessRSS(pids []int) map[int]uint64 {
	out := map[int]uint64{}
	page := uint64(os.Getpagesize())
	for _, p := range pids {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(p) + "/statm")
		if err != nil {
			continue
		}
		f := strings.Fields(string(b))
		if len(f) < 2 {
			continue
		}
		if n, err := strconv.ParseUint(f[1], 10, 64); err == nil {
			out[p] = n * page
		}
	}
	return out
}
