//go:build windows

package pgbundle

import (
	"golang.org/x/sys/windows"
)

// processName is a running process's executable (alive false: no such process; "" with
// alive: one this process may not look at).
func processName(pid int) (name string, alive bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// gone, or another account's (access denied): only a gone one is said so
		return "", err != windows.ERROR_INVALID_PARAMETER
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err == nil && code != 259 { // STILL_ACTIVE
		return "", false
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", true
	}
	return windows.UTF16ToString(buf[:n]), true
}
