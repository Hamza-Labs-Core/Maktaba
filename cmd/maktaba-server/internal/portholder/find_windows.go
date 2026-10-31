//go:build windows

package portholder

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// find asks netstat for the listener's pid, then tasklist for its program's name.
// Both are in System32 on every Windows; their windows stay hidden.
func find(ctx context.Context, port int) (Holder, bool) {
	out, err := quiet(exec.CommandContext(ctx, "netstat", "-ano", "-p", "TCP")).Output()
	if err != nil {
		return Holder{}, false
	}
	pid, ok := parseNetstat(string(out), port)
	if !ok {
		// an IPv6 listener ([::]:9111) is in the TCPv6 table only
		if out, err = quiet(exec.CommandContext(ctx, "netstat", "-ano", "-p", "TCPv6")).Output(); err == nil {
			pid, ok = parseNetstat(string(out), port)
		}
	}
	if !ok {
		return Holder{}, false
	}
	h := Holder{PID: pid}
	if out, err := quiet(exec.CommandContext(ctx, "tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH")).Output(); err == nil {
		h.Command = parseTasklist(string(out))
	}
	return h, true
}

func quiet(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c
}
