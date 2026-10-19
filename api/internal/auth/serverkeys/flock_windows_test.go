//go:build windows

package serverkeys

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Stub audit #35: on Windows the key-dir lock is a real exclusive file
// lock. While one handle holds it, a second handle (as another process
// would open it) cannot lock the file, and once released it can.
func TestFlockWindowsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	open := func() *os.File {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}
	a, b := open(), open()
	if err := flockEx(a); err != nil {
		t.Fatalf("flockEx: %v", err)
	}
	tryLock := func(f *os.File) error {
		var ol windows.Overlapped
		return windows.LockFileEx(windows.Handle(f.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	}
	if err := tryLock(b); err == nil {
		t.Fatal("a second handle locked the file while the first held it")
	}

	// a blocking flockEx on the second handle waits for the release
	got := make(chan error, 1)
	go func() { got <- flockEx(b) }()
	select {
	case err := <-got:
		t.Fatalf("second flockEx returned while the lock was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := flockUn(a); err != nil {
		t.Fatalf("flockUn: %v", err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("second flockEx: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second flockEx never got the lock after release")
	}
	if err := flockUn(b); err != nil {
		t.Fatalf("flockUn second: %v", err)
	}
}

// lockDir serialises two callers on Windows exactly as on unix.
func TestLockDirWindowsSerialises(t *testing.T) {
	dir := t.TempDir()
	release, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan func(), 1)
	go func() {
		r, err := lockDir(dir)
		if err != nil {
			t.Error(err)
			second <- func() {}
			return
		}
		second <- r
	}()
	select {
	case <-second:
		t.Fatal("second lockDir did not wait")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case r := <-second:
		r()
	case <-time.After(5 * time.Second):
		t.Fatal("second lockDir never acquired")
	}
}
