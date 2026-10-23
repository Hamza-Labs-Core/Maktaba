//go:build !windows

package main

import (
	"os"
	"syscall"
)

// reexec runs the launcher at path in this process (its lock is released with the
// files a new program does not inherit, and taken again by it).
func reexec(path string) error {
	return syscall.Exec(path, append([]string{path}, os.Args[1:]...), os.Environ())
}
