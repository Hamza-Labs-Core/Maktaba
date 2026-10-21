//go:build !windows

package supervisor

import "testing"

// ownConsole: POSIX asks a child with a signal, which needs no console; the
// test runs where it is.
func ownConsole(*testing.T) bool { return true }
