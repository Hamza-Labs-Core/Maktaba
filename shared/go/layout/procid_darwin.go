//go:build darwin

package layout

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// BootID is this boot's id (kern.boottime).
func BootID() string {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return ""
	}
	return strconv.FormatInt(tv.Sec, 10) + "." + strconv.FormatInt(int64(tv.Usec), 10)
}

// sZombie is a process that has exited and waits to be reaped (sys/proc.h SZOMB).
const sZombie = 5

// ProcessStart is the start time of a running process in microseconds since the epoch
// (kern.proc.pid's p_starttime). A zombie is not running.
func ProcessStart(pid int) (uint64, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k == nil || int(k.Proc.P_pid) != pid || k.Proc.P_stat == sZombie {
		return 0, ErrNoProcess
	}
	st := k.Proc.P_starttime
	return uint64(st.Sec)*1_000_000 + uint64(st.Usec), nil
}
