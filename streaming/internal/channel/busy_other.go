//go:build !windows

package channel

// isBusy: never off Windows (a file another process has open can be read).
func isBusy(error) bool { return false }
