//go:build linux

package layout

import (
	"os"
	"strconv"
	"strings"
)

// BootID is this boot's id (/proc/sys/kernel/random/boot_id).
func BootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ProcessStart is the start time of a running process, in clock ticks since boot
// (/proc/<pid>/stat's starttime). A process that has exited and waits to be reaped (a
// zombie) is not running.
func ProcessStart(pid int) (uint64, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, ErrNoProcess
	}
	// "pid (comm) state ppid pgrp ...": comm may hold spaces and parentheses
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, ErrNoProcess
	}
	f := strings.Fields(s[i+1:])
	// f[0] is field 3 (state); starttime is field 22
	if len(f) < 20 || f[0] == "Z" || f[0] == "X" {
		return 0, ErrNoProcess
	}
	return strconv.ParseUint(f[19], 10, 64)
}
