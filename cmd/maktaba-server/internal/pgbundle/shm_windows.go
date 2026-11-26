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
// beside them. The directory is read as it may have been written — UTF-8, and the system's
// ANSI code page (a Postgres that writes the lock with the narrow functions names a
// folder outside it in that code page, which never matches read as UTF-8).
func shmInUse(lock string) bool {
	lines := strings.Split(lock, "\n")
	if len(lines) < 2 {
		return false
	}
	for _, data := range shmDataDirs(strings.TrimSpace(lines[1])) {
		name, err := utf16Ptr(shmName(data))
		if err != nil {
			continue
		}
		if mappingThere(name) {
			return true
		}
	}
	return false
}

// utf16Ptr is s as the UTF-16 string the wide API wants.
func utf16Ptr(s string) (*uint16, error) { return windows.UTF16PtrFromString(s) }

// mappingThere reports whether the named mapping exists, or may (this process may not
// look at it: access denied).
func mappingThere(name *uint16) bool {
	const fileMapRead = 4 // FILE_MAP_READ
	h, _, err := procOpenFileMapping.Call(fileMapRead, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	_ = windows.CloseHandle(windows.Handle(h))
	return true
}

// shmDataDirs are the data directory names a lock's line may name: itself, and itself read
// as the system's ANSI code page (the same string when the path is of it: one name).
func shmDataDirs(raw string) []string {
	out := []string{raw}
	if acp := acpToUTF8(raw); acp != "" && acp != raw {
		out = append(out, acp)
	}
	return out
}

// acpToUTF8 is the bytes of s as the system's ANSI code page names them ("" when they
// cannot be read so). MultiByteToWideChar turns one code page's bytes into UTF-16, which
// is the string Go reads the file as when it is not UTF-8.
func acpToUTF8(s string) string {
	b, err := windows.ByteSliceFromString(s)
	if err != nil || len(b) < 2 {
		return ""
	}
	const cpAcp = 0
	n, _, _ := procMultiByteToWideChar.Call(cpAcp, 0, uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)-1), 0, 0)
	if n == 0 {
		return ""
	}
	w := make([]uint16, n)
	n, _, _ = procMultiByteToWideChar.Call(cpAcp, 0, uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)-1), uintptr(unsafe.Pointer(&w[0])), uintptr(len(w)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(w[:n])
}

// shmName is the name Postgres gives a data directory's shared memory (win32_shmem.c,
// GetSharedMemName: every backslash of it a slash, that of "Global" too).
func shmName(data string) string {
	if abs, err := filepath.Abs(filepath.FromSlash(data)); err == nil {
		data = abs
	}
	return strings.ReplaceAll(`Global\PostgreSQL:`+data, `\`, "/")
}

var (
	procOpenFileMapping     = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenFileMappingW")
	procMultiByteToWideChar = windows.NewLazySystemDLL("kernel32.dll").NewProc("MultiByteToWideChar")
)
