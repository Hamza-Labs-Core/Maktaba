//go:build windows

package appvenv

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// hasConsole reports whether this process has a console (a CTRL_BREAK goes
// through one).
func hasConsole() bool {
	var pids [4]uint32
	n, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList").
		Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n != 0
}
