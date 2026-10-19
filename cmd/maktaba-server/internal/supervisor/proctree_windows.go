//go:build windows

package supervisor

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procTree ties a child's descendants (ffmpeg, yt-dlp, ...) to it. On
// Windows the child runs in a Job Object the supervisor owns, with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: closing the job (on restart, on
// stop, or when the supervisor itself dies and the kernel closes its
// handles) terminates every process in it.
type procTree struct {
	mu  sync.Mutex
	job windows.Handle // 0 once released
}

// prepareTree runs before Start: the child is created suspended so it
// cannot spawn anything before it is in the job; attachTree resumes it.
func prepareTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

var procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// attachTree puts the started (suspended) child in a new kill-on-close
// job and resumes it. A job failure still resumes the child (it then
// runs uncontained, and the error is returned for the log); a resume
// failure kills it.
func attachTree(cmd *exec.Cmd) (*procTree, error) {
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("open child process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(ph) }()

	tree, jobErr := newKillOnCloseJob()
	if jobErr == nil {
		if err := windows.AssignProcessToJobObject(tree.job, ph); err != nil {
			tree.release()
			tree, jobErr = nil, fmt.Errorf("assign child to job: %w", err)
		}
	}
	if r, _, _ := procNtResumeProcess.Call(uintptr(ph)); r != 0 {
		tree.kill()
		_ = cmd.Process.Kill()
		return nil, errors.Join(jobErr, fmt.Errorf("resume child: NTSTATUS 0x%x", r))
	}
	return tree, jobErr
}

func newKillOnCloseJob() (*procTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set job limits: %w", err)
	}
	return &procTree{job: job}, nil
}

// kill terminates every process in the job, the child included.
func (t *procTree) kill() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	}
}

// release closes the job after the child has been reaped; kill-on-close
// ends whatever is left in it.
func (t *procTree) release() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
		t.job = 0
	}
}
