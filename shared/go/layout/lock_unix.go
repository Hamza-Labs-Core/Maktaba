//go:build !windows

package layout

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock takes <install>/updates/lock for the life of the process (EC5: a
// second launcher exits with already_running). Release with Unlock (or exit).
func (l Layout) Lock() (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(l.LockPath()), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(l.LockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return f, nil
}

// Unlock releases a lock taken by Lock.
func Unlock(f *os.File) {
	if f != nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}
}
