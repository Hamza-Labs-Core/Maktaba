//go:build !windows

package pgbundle

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// shmInUse reads the segment a postmaster.pid's seventh line names: attached is in use, one
// gone or none holds is free, one this process may not look at counts as in use.
func TestShmInUse(t *testing.T) {
	lock := func(line7 string) string { return "4242\n/d\n1700000000\n5432\n/d\n127.0.0.1\n" + line7 + "\nready\n" }
	old := shmStat
	t.Cleanup(func() { shmStat = old })
	cases := []struct {
		name  string
		lock  string
		stat  func(int, *unix.SysvShmDesc) (int, error)
		inUse bool
	}{
		{"attached", lock("  5432001     65538"), func(id int, ds *unix.SysvShmDesc) (int, error) {
			if id != 65538 {
				t.Fatalf("looked at segment %d", id)
			}
			ds.Nattch = 3
			return 0, nil
		}, true},
		{"none attached", lock("  5432001     65538"), func(_ int, ds *unix.SysvShmDesc) (int, error) { ds.Nattch = 0; return 0, nil }, false},
		{"gone (another IPC namespace)", lock("  5432001     65538"), func(int, *unix.SysvShmDesc) (int, error) { return 0, unix.EINVAL }, false},
		{"not ours to look at", lock("  5432001     65538"), func(int, *unix.SysvShmDesc) (int, error) { return 0, unix.EACCES }, true},
		{"no seventh line", "4242\n/d\n", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shmStat = func(id int, ds *unix.SysvShmDesc) (int, error) {
				if tc.stat == nil {
					t.Fatal("looked at a segment the lock does not name")
				}
				return tc.stat(id, ds)
			}
			if got := shmInUse(tc.lock); got != tc.inUse {
				t.Fatalf("shmInUse = %v, want %v", got, tc.inUse)
			}
		})
	}
}

// A postmaster.pid whose PID is another program now but whose shared memory a process
// still holds (a backend of the postmaster that wrote it) is kept: a real segment, attached.
func TestALockWhoseSharedMemoryIsHeldIsKept(t *testing.T) {
	id, err := unix.SysvShmGet(unix.IPC_PRIVATE, 4096, unix.IPC_CREAT|0o600)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
			t.Skipf("no System V shared memory here: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = unix.SysvShmCtl(id, unix.IPC_RMID, nil) })
	mem, err := unix.SysvShmAttach(id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other := helperProcess(t)
	b := Bundle{Data: t.TempDir()}
	path := filepath.Join(b.Data, "postmaster.pid")
	write := func() {
		t.Helper()
		raw := strconv.Itoa(other) + "\n" + b.Data + "\n1700000000\n5432\n" + b.Data + "\n127.0.0.1\n  5432001 " +
			strconv.Itoa(id) + "\nready\n"
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	b.ClearStaleLock()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a lock whose shared memory is attached was removed")
	}
	if err := unix.SysvShmDetach(mem); err != nil {
		t.Fatal(err)
	}
	b.ClearStaleLock()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a lock whose shared memory none holds was kept")
	}
}
