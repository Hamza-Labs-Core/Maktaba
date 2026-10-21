//go:build !linux

package main

import "syscall"

func orphanAttr() *syscall.SysProcAttr { return nil }
