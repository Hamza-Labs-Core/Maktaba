//go:build windows

package layout

import (
	"strconv"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// BootID is this boot's id: the boot counter Windows keeps (PrefetchParameters BootId),
// else the boot time in seconds (from the time since boot: SameBoot allows for its drift).
func BootID() string {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters`, registry.QUERY_VALUE); err == nil {
		defer func() { _ = k.Close() }()
		if n, _, err := k.GetIntegerValue("BootId"); err == nil && n > 0 {
			return "b" + strconv.FormatUint(n, 10)
		}
	}
	up := windows.DurationSinceBoot()
	return "t" + strconv.FormatInt(time.Now().Add(-up).Unix(), 10)
}

// ProcessStart is the creation time of a running process (GetProcessTimes, in 100 ns
// intervals since 1601). One that has exited is not running.
func ProcessStart(pid int) (uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return 0, ErrNoProcess
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if ev, err := windows.WaitForSingleObject(h, 0); err != nil || ev != uint32(windows.WAIT_TIMEOUT) {
		return 0, ErrNoProcess // signalled: it has exited
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, ErrNoProcess
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}
