//go:build windows

package pgbundle

import (
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// shmInUse: the shared memory of the cluster a postmaster.pid is of (its second line: the
// data directory) still exists, or may (this process may not look at it). Windows frees a
// named mapping with its last handle, so one that is there has a process: a postmaster's, or
// those it left when it was killed. Postgres makes the same check, and refuses to start
// beside them.
func shmInUse(lock string) bool {
	lines := strings.Split(lock, "\n")
	if len(lines) < 2 {
		return false
	}
	name, err := windows.UTF16PtrFromString(shmName(strings.TrimSpace(lines[1])))
	if err != nil {
		return false
	}
	const fileMapRead = 4 // FILE_MAP_READ
	h, _, err := procOpenFileMapping.Call(fileMapRead, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	_ = windows.CloseHandle(windows.Handle(h))
	return true
}

// shmName is the name Postgres gives a data directory's shared memory (win32_shmem.c,
// GetSharedMemName: every backslash of it a slash, that of "Global" too).
func shmName(data string) string {
	if abs, err := filepath.Abs(filepath.FromSlash(data)); err == nil {
		data = abs
	}
	return strings.ReplaceAll(`Global\PostgreSQL:`+data, `\`, "/")
}

var procOpenFileMapping = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenFileMappingW")
