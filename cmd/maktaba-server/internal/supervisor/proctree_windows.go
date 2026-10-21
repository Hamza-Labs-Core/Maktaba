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
// handles) terminates every process in it. It also holds a handle to the
// child until it is reaped, so the child's pid, which askChild names, is
// never another process's while the supervisor can still ask.
type procTree struct {
	mu   sync.Mutex
	job  windows.Handle // 0 once released, or when no job could be made
	proc windows.Handle // the child; 0 once released
}

// prepareTree runs before Start: the child is created suspended so it
// cannot spawn anything before it is in the job (attachTree resumes it), and
// in a process group of its own (group id = its pid), which askChild's
// CTRL_BREAK reaches without the supervisor or the other roles. CTRL_C is
// disabled in a new group, so a Ctrl+C in serve's console reaches serve only,
// which then stops the children in their order.
//
// Without a console of its own (a bare serve started detached by a GUI
// program) each console child would get a console with a window on the
// desktop: CREATE_NO_WINDOW gives it one without. Not while there is one:
// the flag also gives the child a console of its own, which a CTRL_BREAK on
// this one does not reach.
func prepareTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP)
	if !hasConsole() {
		flags |= windows.CREATE_NO_WINDOW
	}
	cmd.SysProcAttr.CreationFlags |= flags
}

// hasConsole: this process has a console its children can share (a var for
// the tests).
var hasConsole = func() bool {
	_, err := windows.GetConsoleCP()
	return err == nil
}

var (
	ntdll                = windows.NewLazySystemDLL("ntdll.dll")
	procNtResumeProcess  = ntdll.NewProc("NtResumeProcess")
	procNtSuspendProcess = ntdll.NewProc("NtSuspendProcess")
)

// attachTree puts the started (suspended) child in a new kill-on-close
// job and resumes it. A job failure still resumes the child (it then
// runs uncontained, and the error is returned for the log); a resume
// failure kills it.
func attachTree(cmd *exec.Cmd) (*procTree, error) {
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME|
		windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("open child process: %w", err)
	}
	tree := &procTree{proc: ph}
	job, jobErr := newKillOnCloseJob()
	if jobErr == nil {
		if err := windows.AssignProcessToJobObject(job, ph); err != nil {
			_ = windows.CloseHandle(job)
			jobErr = fmt.Errorf("assign child to job: %w", err)
		} else {
			tree.job = job
		}
	}
	if r, _, _ := procNtResumeProcess.Call(uintptr(ph)); r != 0 {
		tree.kill()
		_ = cmd.Process.Kill()
		tree.release()
		return nil, errors.Join(jobErr, fmt.Errorf("resume child: NTSTATUS 0x%x", r))
	}
	return tree, jobErr
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("set job limits: %w", err)
	}
	return job, nil
}

// ask runs send while the child still runs: the handle held keeps its pid
// from being reused meanwhile, and the child is held suspended from the check
// to the send, so it cannot exit in between. The event names the group the
// child leads; one naming a group nobody leads goes to every process of the
// console, the supervisor included. The child's handler runs once it is
// resumed. A child that has exited needs no asking; one that cannot be held
// is not asked (the caller kills it instead).
func (t *procTree) ask(send func() error) error {
	if t == nil {
		return nil // attachTree failed and killed the child
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.proc == 0 || t.exited() {
		return nil
	}
	if r, _, _ := procNtSuspendProcess.Call(uintptr(t.proc)); r != 0 {
		if t.exited() {
			return nil
		}
		return fmt.Errorf("hold the child to ask it: NTSTATUS 0x%x", r)
	}
	defer procNtResumeProcess.Call(uintptr(t.proc))
	if t.exited() {
		return nil
	}
	return send()
}

func (t *procTree) exited() bool {
	ev, err := windows.WaitForSingleObject(t.proc, 0)
	return err == nil && ev == windows.WAIT_OBJECT_0
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
	if t.proc != 0 {
		_ = windows.CloseHandle(t.proc)
		t.proc = 0
	}
}
