//go:build windows

package serverkeys

import (
	"math"
	"os"

	"golang.org/x/sys/windows"
)

// flockEx takes an exclusive lock on the whole of f, blocking until no
// other process holds it (LockFileEx with LOCKFILE_EXCLUSIVE_LOCK and no
// FAIL_IMMEDIATELY), the Windows counterpart of flock(LOCK_EX). Two
// processes generating keys at first boot then serialise as on unix.
func flockEx(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0,
		math.MaxUint32, math.MaxUint32, &ol)
}

// flockUn releases the lock flockEx took.
func flockUn(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, math.MaxUint32, math.MaxUint32, &ol)
}
