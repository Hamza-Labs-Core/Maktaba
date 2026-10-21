//go:build !windows

package appvenv

func hasConsole() bool { return true }
