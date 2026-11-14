//go:build !windows

package pgbundle

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// shmWaitFakes sets a cluster whose postmaster.pid names postmaster 4242 and segment
// 65538: alive says whether that process runs, attached how many hold the segment.
func shmWaitFakes(t *testing.T, limit time.Duration) (b Bundle, alive *atomic.Bool, attached *atomic.Int32) {
	t.Helper()
	oldLook, oldStat, oldWait := lookProcess, shmStat, shmWait
	t.Cleanup(func() { lookProcess, shmStat, shmWait = oldLook, oldStat, oldWait })
	alive, attached = &atomic.Bool{}, &atomic.Int32{}
	lookProcess = func(pid int) (string, bool) {
		if pid != 4242 {
			t.Errorf("looked at process %d", pid)
		}
		if alive.Load() {
			return "postgres", true
		}
		return "", false
	}
	shmStat = func(id int, ds *unix.SysvShmDesc) (int, error) {
		if id != 65538 {
			t.Errorf("looked at segment %d", id)
		}
		ds.Nattch = 0 // its width differs by system
		if attached.Load() > 0 {
			ds.Nattch = 1
		}
		return 0, nil
	}
	shmWait = limit
	b = Bundle{Data: t.TempDir()}
	lock := "4242\n" + b.Data + "\n1700000000\n5432\n" + b.Data + "\n127.0.0.1\n  5432001     65538\nready\n"
	if err := os.WriteFile(filepath.Join(b.Data, "postmaster.pid"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	return b, alive, attached
}

// A start waits while backends of a killed postmaster still hold its shared memory
// (Postgres refuses to start next to them), and goes on as soon as they are gone.
func TestAwaitShmFreeWaitsForTheBackendsAKilledPostmasterLeft(t *testing.T) {
	b, _, attached := shmWaitFakes(t, 30*time.Second)
	attached.Store(2)
	const held = 400 * time.Millisecond
	time.AfterFunc(held, func() { attached.Store(0) })
	start := time.Now()
	b.AwaitShmFree(context.Background())
	if d := time.Since(start); d < held || d > 10*time.Second {
		t.Fatalf("waited %s for memory held %s", d, held)
	}
	if attached.Load() != 0 {
		t.Fatal("returned while the memory was still held")
	}
}

// The wait has a limit (the start then says why it fails), ends with the context, and is
// not made at all for memory none holds, a postmaster that still runs or no lock.
func TestAwaitShmFreeBounds(t *testing.T) {
	within := func(t *testing.T, limit time.Duration, f func()) time.Duration {
		t.Helper()
		start := time.Now()
		f()
		d := time.Since(start)
		if d > limit {
			t.Fatalf("took %s, want under %s", d, limit)
		}
		return d
	}
	t.Run("its limit", func(t *testing.T) {
		b, _, attached := shmWaitFakes(t, 300*time.Millisecond)
		attached.Store(1)
		if d := within(t, 5*time.Second, func() { b.AwaitShmFree(context.Background()) }); d < 300*time.Millisecond {
			t.Fatalf("gave up after %s, before its limit", d)
		}
	})
	t.Run("the context ends", func(t *testing.T) {
		b, _, attached := shmWaitFakes(t, time.Minute)
		attached.Store(1)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		within(t, 5*time.Second, func() { b.AwaitShmFree(ctx) })
	})
	t.Run("none holds it", func(t *testing.T) {
		b, _, _ := shmWaitFakes(t, time.Minute)
		within(t, time.Second, func() { b.AwaitShmFree(context.Background()) })
	})
	t.Run("the postmaster still runs", func(t *testing.T) {
		b, alive, attached := shmWaitFakes(t, time.Minute)
		alive.Store(true)
		attached.Store(5)
		within(t, time.Second, func() { b.AwaitShmFree(context.Background()) })
	})
	t.Run("no lock", func(t *testing.T) {
		b, _, attached := shmWaitFakes(t, time.Minute)
		attached.Store(1)
		if err := os.Remove(filepath.Join(b.Data, "postmaster.pid")); err != nil {
			t.Fatal(err)
		}
		within(t, time.Second, func() { b.AwaitShmFree(context.Background()) })
	})
}
