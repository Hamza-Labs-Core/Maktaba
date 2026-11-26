//go:build !windows

package pgbundle

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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

// TestShmCreatorHelper is the postmaster that was killed: it makes its cluster's shared
// memory segment (as its creator) and attaches to it (run only as a child).
func TestShmCreatorHelper(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_SHM_CREATOR") != "1" {
		t.Skip("a helper process")
	}
	id, err := unix.SysvShmGet(unix.IPC_PRIVATE, 4096, unix.IPC_CREAT|0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := unix.SysvShmAttach(id, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(id)
	time.Sleep(30 * time.Second)
}

// A postmaster.pid whose PID is another program now but whose shared memory a process
// still holds (a backend of the postmaster that wrote it, the segment's creator) is kept:
// a real segment, attached. A segment that another postmaster made (the id a stale lock
// names, taken since) frees the lock: waiting for it would be a wait for another server.
func TestALockWhoseSharedMemoryIsHeldIsKept(t *testing.T) {
	id, err := unix.SysvShmGet(unix.IPC_PRIVATE, 4096, unix.IPC_CREAT|0o600)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
			t.Skipf("no System V shared memory here: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = unix.SysvShmCtl(id, unix.IPC_RMID, nil) })
	// the postmaster that wrote the lock: the segment's creator, still running
	maker := exec.Command(os.Args[0], "-test.run=^TestShmCreatorHelper$")
	maker.Env = append(os.Environ(), "MAKTABA_TEST_SHM_CREATOR=1")
	out, err := maker.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := maker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = maker.Process.Kill(); _ = maker.Wait() })
	var made int
	if _, err := fmt.Fscan(out, &made); err != nil {
		t.Fatalf("the maker's segment: %v", err)
	}
	t.Cleanup(func() { _, _ = unix.SysvShmCtl(made, unix.IPC_RMID, nil) })
	b := Bundle{Data: t.TempDir()}
	path := filepath.Join(b.Data, "postmaster.pid")
	write := func(pid, segment int) {
		t.Helper()
		raw := strconv.Itoa(pid) + "\n" + b.Data + "\n1700000000\n5432\n" + b.Data + "\n127.0.0.1\n  5432001 " +
			strconv.Itoa(segment) + "\nready\n"
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(maker.Process.Pid, made)
	b.ClearStaleLock()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a lock whose shared memory is attached was removed")
	}
	// a segment another process made (the lock's id taken since) frees the lock, held
	// though it is
	mem, err := unix.SysvShmAttach(id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	write(maker.Process.Pid, id)
	b.ClearStaleLock()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a lock naming a segment another postmaster made was kept")
	}
	if err := unix.SysvShmDetach(mem); err != nil {
		t.Fatal(err)
	}
	// none holds it: the lock goes (a program alive in its PID, so Postgres is not asked)
	write(maker.Process.Pid, made)
	_ = maker.Process.Kill()
	_ = maker.Wait()
	other := helperProcess(t)
	write(other, made)
	b.ClearStaleLock()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a lock whose shared memory none holds was kept")
	}
}
