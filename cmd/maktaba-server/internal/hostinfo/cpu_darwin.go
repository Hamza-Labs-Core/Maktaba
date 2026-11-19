//go:build darwin

package hostinfo

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ProcessCPU is the processor time (user and system) each running pid has used since it
// started (ps's cputime: macOS has no /proc, and the call that reads it needs cgo). A pid
// that is gone has no entry (story 54.52).
func ProcessCPU(pids []int) map[int]time.Duration {
	out := map[int]time.Duration{}
	list := make([]string, 0, len(pids))
	for _, p := range pids {
		if p > 0 && p <= pidMax { // ps refuses the whole list for one out of range
			list = append(list, strconv.Itoa(p))
		}
	}
	if len(list) == 0 {
		return out
	}
	b, _ := exec.Command("/bin/ps", "-o", "pid=,cputime=", "-p", strings.Join(list, ",")).Output()
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		d, ok := parsePSTime(f[1])
		if err == nil && ok {
			out[pid] = d
		}
	}
	return out
}
