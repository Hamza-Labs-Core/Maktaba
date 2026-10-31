//go:build unix

package portholder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func find(ctx context.Context, port int) (Holder, bool) {
	if h, ok := lsof(ctx, port); ok {
		return h, true
	}
	if _, err := os.Stat("/proc/net/tcp"); err == nil {
		return fromProc("/proc", port)
	}
	return Holder{}, false
}

// lsof asks lsof for the listener: -n -P keep it from resolving names (fast), -F
// pc prints the pid and the command only.
func lsof(ctx context.Context, port int) (Holder, bool) {
	bin, err := exec.LookPath("lsof")
	if err != nil {
		bin = "/usr/sbin/lsof" // a Mac's, when PATH is the launchd one
		if _, err := os.Stat(bin); err != nil {
			return Holder{}, false
		}
	}
	out, _ := exec.CommandContext(ctx, bin, "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc").Output()
	return parseLsof(string(out))
}

// fromProc finds the listener in /proc (Linux without lsof): the socket's inode in
// net/tcp or tcp6, then the process with a descriptor on it. Another user's
// processes stay hidden unless we run as root.
func fromProc(root string, port int) (Holder, bool) {
	var inode string
	for _, t := range []string{"net/tcp", "net/tcp6"} {
		b, err := os.ReadFile(filepath.Join(root, t))
		if err != nil {
			continue
		}
		if in, ok := parseProcNetTCP(string(b), port); ok {
			inode = in
			break
		}
	}
	if inode == "" || inode == "0" {
		return Holder{}, false
	}
	target := "socket:[" + inode + "]"
	dirs, _ := os.ReadDir(root)
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(root, d.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if l, err := os.Readlink(filepath.Join(root, d.Name(), "fd", fd.Name())); err == nil && l == target {
				comm, _ := os.ReadFile(filepath.Join(root, d.Name(), "comm"))
				return Holder{PID: pid, Command: strings.TrimSpace(string(comm))}, true
			}
		}
	}
	return Holder{}, false
}
