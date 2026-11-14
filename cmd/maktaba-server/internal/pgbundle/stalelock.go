package pgbundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ClearStaleLock removes the cluster's postmaster.pid when the process it names is
// another program: one the system gave the PID again after a postmaster that did not
// end cleanly (a power cut, a machine switched off). Postgres refuses to start while
// the PID runs, whatever it is. A lock whose PID is no process is left to Postgres (it
// takes it over). One whose PID is a postgres nobody runs any more (its server was
// killed and the system adopted it: macOS, where no launcher reaps what the server left)
// is stopped first, or Postgres would refuse to start forever; any other postgres is left
// to Postgres (it says another postmaster runs). A lock whose shared memory a process
// still holds (a backend of the postmaster that wrote it) is never removed: Postgres's own
// check, which a removed lock would skip. Then the socket's lock: clearStaleSocketLock.
func (b Bundle) ClearStaleLock() {
	b.clearStaleDataLock()
	b.clearStaleSocketLock()
}

// clearStaleDataLock is ClearStaleLock's postmaster.pid.
func (b Bundle) clearStaleDataLock() {
	path := filepath.Join(b.Data, "postmaster.pid")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return
	}
	name, alive := lookProcess(pid)
	if !alive || name == "" {
		return
	}
	if isPostgres(name) {
		if adopted(pid) {
			stopLeftover(pid, path)
		}
		return
	}
	if shmInUse(string(raw)) {
		fmt.Fprintf(os.Stderr, "[postgres] %s names process %d (%s now), but its shared memory is still in use: kept\n",
			path, pid, name)
		return
	}
	if err := os.Remove(path); err == nil {
		fmt.Fprintf(os.Stderr, "[postgres] %s named process %d, which is %s now (the postmaster that wrote it did not "+
			"end cleanly): removed\n", path, pid, name)
	}
}

// clearStaleSocketLock removes the unix socket's lock (.s.PGSQL.<port>.lock, in the data
// directory) when its PID is a live process but no postmaster holds this cluster. Postgres
// takes over a socket lock only when its PID is no process (or its own, or its parent's):
// it has no shared memory check for this one. In a container started again after a kill
// (a power cut, docker kill), the old postmaster's PID is soon another process's in the
// new PID namespace, and Postgres refused to start forever ("lock file already exists").
// A postmaster takes its data directory's lock (postmaster.pid) before its socket's, so
// while postmaster.pid names no live postgres (clearStaleDataLock has run) no postmaster
// owns this socket: the lock is left from the one that did not end cleanly.
func (b Bundle) clearStaleSocketLock() {
	if b.Port <= 0 {
		return
	}
	path := filepath.Join(b.Data, ".s.PGSQL."+strconv.Itoa(b.Port)+".lock")
	pid, ok := lockPID(path)
	if !ok || pid == os.Getpid() {
		return
	}
	if _, alive := lookProcess(pid); !alive || b.holdsData() {
		return
	}
	if err := os.Remove(path); err == nil {
		fmt.Fprintf(os.Stderr, "[postgres] %s named process %d, but no postmaster holds %s (the one that wrote it "+
			"did not end cleanly): removed\n", path, pid, b.Data)
	}
}

// AwaitShmFree waits, before a start, while the cluster's postmaster.pid names a
// postmaster that is gone and the shared memory it made still has a process attached:
// backends it left when it was killed, which end on their own once they notice (one in
// the middle of a slow write takes its time). Postgres refuses to start next to them
// ("pre-existing shared memory block is still in use"), and a server started at once
// failed four times in a row that way: a crash loop, and five minutes without a database.
// It returns when the memory is free, after shmWait (the start then says why it fails),
// or when ctx ends. A lock whose PID still runs is not this case: ClearStaleLock's, or
// Postgres's own to refuse.
func (b Bundle) AwaitShmFree(ctx context.Context) {
	path := filepath.Join(b.Data, "postmaster.pid")
	start := time.Now()
	said := false
	for {
		raw, err := os.ReadFile(path)
		if err != nil {
			return
		}
		pid, ok := lockPID(path)
		if !ok {
			return
		}
		_, alive := lookProcess(pid)
		if alive || !shmInUse(string(raw)) {
			if said && !alive {
				fmt.Fprintf(os.Stderr, "[postgres] the shared memory of postmaster %d is free after %s\n",
					pid, time.Since(start).Round(time.Millisecond))
			}
			return
		}
		if !said {
			said = true
			fmt.Fprintf(os.Stderr, "[postgres] postmaster %d is gone, but processes it left still hold its shared memory: "+
				"waiting up to %s for them to end before the start\n", pid, shmWait)
		}
		if time.Since(start) >= shmWait {
			fmt.Fprintf(os.Stderr, "[postgres] the shared memory of postmaster %d is still in use after %s: starting anyway\n",
				pid, shmWait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// lockPID is the PID on a lock file's first line (ok false: no file, or no PID).
func lockPID(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	return pid, err == nil && pid > 0
}

// holdsData: the cluster's postmaster.pid names a live process that is a postgres, or one
// this system cannot name (Running, through the lookups tests replace).
func (b Bundle) holdsData() bool {
	pid, ok := lockPID(filepath.Join(b.Data, "postmaster.pid"))
	if !ok {
		return false
	}
	name, alive := lookProcess(pid)
	return alive && (name == "" || isPostgres(name))
}

// The process lookups ClearStaleLock makes (tests replace them).
var (
	lookProcess  = processName
	parentOf     = parentPID
	signalPID    = signalProcess
	leftoverWait = 30 * time.Second
	// shmWait is how long AwaitShmFree waits for a gone postmaster's backends to end.
	shmWait = 2 * time.Minute
)

// adopted: the process's parent is the system's first process, so whatever started
// it (the server, which runs every postmaster of its clusters in the foreground) is
// gone. Only one server runs per data directory (the launcher's guard), so such a
// postmaster of this cluster is one the last server left.
func adopted(pid int) bool {
	ppid, ok := parentOf(pid)
	return ok && ppid == 1
}

// stopLeftover asks a postmaster the last server left to shut down fast (it
// checkpoints, so the next start needs no crash recovery), and kills it when it has
// not ended after leftoverWait. Postgres takes over the lock of a gone postmaster.
func stopLeftover(pid int, lock string) {
	fmt.Fprintf(os.Stderr, "[postgres] %s names postgres %d, which the last server left running: stopping it\n", lock, pid)
	if signalPID(pid, syscall.SIGINT) != nil {
		return
	}
	if goneWithin(pid, leftoverWait) {
		return
	}
	fmt.Fprintf(os.Stderr, "[postgres] postgres %d did not stop in %s: killed\n", pid, leftoverWait)
	_ = signalPID(pid, syscall.SIGKILL)
	goneWithin(pid, 5*time.Second)
}

// goneWithin waits up to d for the process to end.
func goneWithin(pid int, d time.Duration) bool {
	for end := time.Now().Add(d); ; time.Sleep(50 * time.Millisecond) {
		if _, alive := lookProcess(pid); !alive {
			return true
		}
		if time.Now().After(end) {
			return false
		}
	}
}

func isPostgres(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return base == "postgres" || base == "postgres.exe" || strings.HasPrefix(base, "postgres:")
}

// Running reports whether a postmaster of this cluster runs: its postmaster.pid
// names a live process that is a postgres (or one this system cannot name). A
// storage move copies the cluster only while none does.
func (b Bundle) Running() bool {
	raw, err := os.ReadFile(filepath.Join(b.Data, "postmaster.pid"))
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 {
		return false
	}
	name, alive := processName(pid)
	return alive && (name == "" || isPostgres(name))
}
