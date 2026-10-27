package pgbundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ClearStaleLock removes the cluster's postmaster.pid when the process it names is
// another program: one the system gave the PID again after a postmaster that did not
// end cleanly (a power cut, a machine switched off). Postgres refuses to start while
// the PID runs, whatever it is. A lock whose PID is a postgres, or no process, is left to
// Postgres (it starts, or says another postmaster runs).
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
	name, alive := processName(pid)
	if !alive || name == "" || isPostgres(name) {
		return
	}
	if err := os.Remove(path); err == nil {
		fmt.Fprintf(os.Stderr, "[postgres] %s named process %d, which is %s now (the postmaster that wrote it did not "+
			"end cleanly): removed\n", path, pid, name)
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
