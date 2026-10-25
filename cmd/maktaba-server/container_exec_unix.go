//go:build !windows

package main

import "syscall"

// execProcess replaces this process with path (the container command's given command).
func execProcess(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }
