//go:build !windows

package supervisor

import (
	"os/exec"
	"sync"
	"syscall"
)

// procTree ties a child's descendants (ffmpeg, yt-dlp, ...) to it. On
// POSIX the child leads its own process group, so killing the group
// takes every descendant that did not move itself out of it.
type procTree struct {
	mu   sync.Mutex
	pgid int // 0 once released
}

// prepareTree runs before Start: the child becomes a process-group
// leader (pgid = its pid).
func prepareTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// attachTree runs right after Start.
func attachTree(cmd *exec.Cmd) (*procTree, error) {
	return &procTree{pgid: cmd.Process.Pid}, nil
}

// kill SIGKILLs the whole group, the child included.
func (t *procTree) kill() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pgid > 0 {
		_ = syscall.Kill(-t.pgid, syscall.SIGKILL)
	}
}

// release runs after the child has been reaped: whatever is left in its
// group (orphaned grandchildren) is killed, and the group is forgotten
// so a later kill cannot hit a reused id.
func (t *procTree) release() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pgid > 0 {
		_ = syscall.Kill(-t.pgid, syscall.SIGKILL)
		t.pgid = 0
	}
}
