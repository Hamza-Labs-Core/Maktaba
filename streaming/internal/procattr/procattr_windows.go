//go:build windows

package procattr

import (
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Tie is a no-op on Windows: children inherit the job ContainSelf set up.
func Tie(*exec.Cmd) {}

var (
	once    sync.Once
	selfJob windows.Handle // held for the life of the process, never closed
	selfErr error
)

// ContainSelf puts this process in a kill-on-close job, once. Processes
// started afterwards are in it too and die with this one.
func ContainSelf() error {
	once.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			selfErr = fmt.Errorf("create job object: %w", err)
			return
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(job)
			selfErr = fmt.Errorf("set job limits: %w", err)
			return
		}
		if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
			_ = windows.CloseHandle(job)
			selfErr = fmt.Errorf("assign self to job: %w", err)
			return
		}
		selfJob = job
	})
	return selfErr
}

// Contained reports whether ContainSelf put this process in its job.
func Contained() bool { return selfJob != 0 }
