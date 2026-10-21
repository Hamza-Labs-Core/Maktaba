//go:build !windows

package appruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// ErrLocked: another install or activation of this runtime kept its lock for
// longer than the wait.
var ErrLocked = errors.New("locked")

// lockWait bounds how long a second install waits for the first.
var lockWait = 2 * time.Minute

// lock takes <root>/lock (flock), waiting for another holder up to lockWait.
func (r Runtime) lock(ctx context.Context) (func(), error) {
	f, err := os.OpenFile(filepath.Join(r.Root, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%w: another install of %s is running", ErrLocked, r.Root)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// freeBytes is the space an unprivileged user can still write on dir's disk.
func freeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// syncAll flushes the copy to the disk before it is renamed into place.
func syncAll() { unix.Sync() }
