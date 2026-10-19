//go:build !unix

package agent

// setUmask is a no-op where there is no umask.
func setUmask(int) int { return 0 }
