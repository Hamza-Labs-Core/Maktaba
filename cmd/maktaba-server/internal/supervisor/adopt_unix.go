//go:build !windows

package supervisor

import (
	"os"
	"os/exec"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// tvJob is Windows' (the boot's named job); POSIX children already lead their own
// process groups (22.9 AC12), which outlive the supervisor as they are.
type tvJob struct{}

// openTVJob: nothing to open on POSIX.
func openTVJob(string, int) (*tvJob, error) { return &tvJob{}, nil }

// held: POSIX needs no handle kept for the children to outlive the supervisor.
func (*tvJob) held() bool { return true }

// sweep: what POSIX leaves behind is the launcher's (it stops a server's orphans).
func (*tvJob) sweep(func(pid int) bool) {}

// prepareSurvivor: a POSIX child needs nothing more to outlive the supervisor.
func prepareSurvivor(*exec.Cmd) {}

// attachSurvivor is attachTree: the child leads its process group, which the next
// supervisor signals by its id. detached reports it may outlive the supervisor.
func attachSurvivor(cmd *exec.Cmd, _ *tvJob, _ string, _ int) (tree *procTree, detached bool, err error) {
	tree, err = attachTree(cmd)
	return tree, true, err
}

// survivorJob: POSIX children have no named job.
func survivorJob(string, int) string { return "" }

// errBreakaway: POSIX has no job to leave.
func errBreakaway(error) bool { return false }

// inChildJob: POSIX children have no named job.
func inChildJob(string, int) bool { return false }

// descendants: POSIX sweeps no job.
func descendants([]int) map[int]bool { return nil }

// heldOf: POSIX children have no job a launcher holds.
func heldOf(*procTree) uint64 { return 0 }

// adoptTree ties an adopted child's process group to it (the group it leads).
func adoptTree(c layout.HandoverChild, _ string, _ int) (*procTree, *os.Process, error) {
	p, err := os.FindProcess(c.PID)
	if err != nil {
		return nil, nil, err
	}
	pgid := c.PGID
	if pgid <= 0 {
		pgid = c.PID
	}
	return &procTree{pgid: pgid}, p, nil
}

// watchGone returns once the adopted process (pid, started at start) is gone: it is not
// this process's child, so it cannot be waited for, only watched.
func watchGone(pid int, start uint64, _ *procTree) {
	for layout.ProcessIs(pid, start) {
		time.Sleep(adoptPoll)
	}
}
