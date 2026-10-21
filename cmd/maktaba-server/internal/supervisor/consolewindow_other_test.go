//go:build !windows

package supervisor

// hasConsoleWindow: a console window is Windows' only.
func hasConsoleWindow() bool { return false }
