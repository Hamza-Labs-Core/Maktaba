//go:build windows

package layout

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Lock takes <install>/updates/lock for the life of the process (EC5).
func (l Layout) Lock() (*os.File, error) { return LockFile(l.LockPath()) }

// LockFile takes an exclusive lock on path (made, with its folder, when
// missing) until Unlock or exit (LockFileEx); ErrLocked when another holder
// has it.
func LockFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return f, nil
}

// Unlock releases a lock taken by Lock.
func Unlock(f *os.File) {
	if f != nil {
		ol := new(windows.Overlapped)
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
		_ = f.Close()
	}
}
