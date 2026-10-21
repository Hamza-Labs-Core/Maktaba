//go:build !windows

package config

import "syscall"

// errCrossDevice is what a rename to another volume fails with.
var errCrossDevice error = syscall.EXDEV
