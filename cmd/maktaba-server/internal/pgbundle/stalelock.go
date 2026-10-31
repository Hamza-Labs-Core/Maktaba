package pgbundle

import (
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
// to Postgres (it says another postmaster runs).
func (b Bundle) ClearStaleLock() {
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
	if err := os.Remove(path); err == nil {
		fmt.Fprintf(os.Stderr, "[postgres] %s named process %d, which is %s now (the postmaster that wrote it did not "+
			"end cleanly): removed\n", path, pid, name)
	}
}

// The process lookups ClearStaleLock makes (tests replace them).
var (
	lookProcess  = processName
	parentOf     = parentPID
	signalPID    = signalProcess
	leftoverWait = 30 * time.Second
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
