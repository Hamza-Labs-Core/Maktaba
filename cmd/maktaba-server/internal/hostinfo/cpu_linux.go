//go:build linux

package hostinfo

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTick is the kernel's USER_HZ, the unit of /proc/<pid>/stat's times: 100 on every
// Linux the server runs on (sysconf(_SC_CLK_TCK) needs cgo).
const clockTick = 100

// ProcessCPU is the processor time (user and system) each running pid has used since it
// started (/proc/<pid>/stat utime and stime). A pid that is gone has no entry (story 54.52).
func ProcessCPU(pids []int) map[int]time.Duration {
	out := map[int]time.Duration{}
	for _, p := range pids {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(p) + "/stat")
		if err != nil {
			continue
		}
		if d, ok := statCPU(string(b)); ok {
			out[p] = d
		}
	}
	return out
}

// statCPU reads utime and stime (fields 14 and 15) of a stat line; the command name in
// parentheses can hold spaces, so the fields are counted after its last ')'.
func statCPU(line string) (time.Duration, bool) {
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(line[i+1:])
	// f[0] is field 3 (state): utime is field 14, f[11]
	if len(f) < 13 {
		return 0, false
	}
	u, err1 := strconv.ParseUint(f[11], 10, 64)
	s, err2 := strconv.ParseUint(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	// a tick is 10 ms exactly: multiplying by it, not by a second, cannot overflow for any
	// time a process can have used (a second first overflows after about 2.9 years of it)
	return time.Duration(u+s) * (time.Second / clockTick), true
}
