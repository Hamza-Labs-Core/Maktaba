//go:build windows

package pgbundle

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A start on Windows waits while the shared memory of a killed postmaster is still there (a
// process it left holds it), and goes on once it is gone.
func TestAwaitShmFreeWaitsForAMapping(t *testing.T) {
	oldLook, oldWait := lookProcess, shmWait
	t.Cleanup(func() { lookProcess, shmWait = oldLook, oldWait })
	lookProcess = func(int) (string, bool) { return "", false }
	shmWait = 30 * time.Second
	b := Bundle{Data: t.TempDir()}
	lock := "4242\n" + filepath.ToSlash(b.Data) + "\n1700000000\n" + strconv.Itoa(5432) + "\n"
	if err := os.WriteFile(filepath.Join(b.Data, "postmaster.pid"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	if shmInUse(lock) {
		t.Fatal("in use with no mapping there")
	}
	name, err := windows.UTF16PtrFromString(shmName(b.Data))
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, 4096, name)
	if err != nil {
		t.Fatal(err)
	}
	if !shmInUse(lock) {
		t.Fatalf("%s is not in use while it is held", shmName(b.Data))
	}
	const held = 400 * time.Millisecond
	time.AfterFunc(held, func() { _ = windows.CloseHandle(h) })
	start := time.Now()
	b.AwaitShmFree(context.Background())
	if d := time.Since(start); d < held-50*time.Millisecond || d > 10*time.Second {
		t.Fatalf("waited %s for memory held %s", d, held)
	}
}
