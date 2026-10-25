//go:build windows

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// orphanStopGrace: Windows ends the TV processes through their job at once.
var orphanStopGrace = 10 * time.Second

var procOpenJobObjectW = kernel32.NewProc("OpenJobObjectW")

// jobObjectTerminate is the job access right TerminateJobObject needs (winnt.h).
const jobObjectTerminate = 0x0008

// stopListed: the TV processes are in the boot's job, which stopTVJob ends.
func stopListed(*layout.Handover, time.Duration) {}

// stopTVJob ends every process in the boot's named job of this install (the TV
// processes the server kept there, and their descendants), if it exists.
func stopTVJob(updatesDir string) {
	name, err := windows.UTF16PtrFromString(layout.TVJobName(updatesDir))
	if err != nil {
		return
	}
	h, _, _ := procOpenJobObjectW.Call(uintptr(jobObjectTerminate), 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return
	}
	_ = windows.TerminateJobObject(windows.Handle(h), 1)
	_ = windows.CloseHandle(windows.Handle(h))
}
