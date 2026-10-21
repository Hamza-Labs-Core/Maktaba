//go:build windows

package supervisor

import "golang.org/x/sys/windows"

var procGetConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// hasConsoleWindow: the fake child's console shows a window on the desktop.
func hasConsoleWindow() bool {
	w, _, _ := procGetConsoleWindow.Call()
	return w != 0
}
