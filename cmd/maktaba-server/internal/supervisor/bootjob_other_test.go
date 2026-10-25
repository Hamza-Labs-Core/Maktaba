//go:build !windows

package supervisor

// bootJobPossible: POSIX has no boot job (its children lead their own process groups).
func bootJobPossible() bool { return false }
