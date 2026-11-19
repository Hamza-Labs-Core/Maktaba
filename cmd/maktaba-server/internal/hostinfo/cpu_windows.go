//go:build windows

package hostinfo

import (
	"time"

	"golang.org/x/sys/windows"
)

// ProcessCPU is the processor time (user and kernel) each running pid has used since it
// started (GetProcessTimes). A pid that is gone (or cannot be opened) has no entry (story
// 54.52).
func ProcessCPU(pids []int) map[int]time.Duration {
	out := map[int]time.Duration{}
	for _, p := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p))
		if err != nil {
			continue
		}
		var created, exited, kernel, user windows.Filetime
		if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
			out[p] = filetime(kernel) + filetime(user)
		}
		_ = windows.CloseHandle(h)
	}
	return out
}

// filetime is a FILETIME duration (100 ns units).
func filetime(f windows.Filetime) time.Duration {
	return time.Duration(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) * 100
}
