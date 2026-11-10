//go:build !windows

package pgbundle

import (
	"errors"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// shmInUse: the shared memory segment a postmaster.pid names (its seventh line: the key
// and the segment's id) still has a process attached, or may (this process may not look
// at it). Postgres makes the same check before it takes over a lock whose PID is gone. A
// segment that no longer exists (a new container has its own) or that none holds is free;
// so is a lock without that line (a postmaster that died before it made the segment).
func shmInUse(lock string) bool {
	lines := strings.Split(lock, "\n")
	if len(lines) < 7 {
		return false
	}
	fields := strings.Fields(lines[6])
	if len(fields) < 2 {
		return false
	}
	id, err := strconv.Atoi(fields[1])
	if err != nil || id < 0 {
		return false
	}
	var ds unix.SysvShmDesc
	if _, err := shmStat(id, &ds); err != nil {
		return !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EIDRM)
	}
	return ds.Nattch > 0
}

// shmStat reads a segment's state (tests replace it).
var shmStat = func(id int, ds *unix.SysvShmDesc) (int, error) { return unix.SysvShmCtl(id, unix.IPC_STAT, ds) }
