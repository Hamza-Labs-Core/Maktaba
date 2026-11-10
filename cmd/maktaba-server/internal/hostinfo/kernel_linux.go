//go:build linux

package hostinfo

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func uname() Uname {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return Uname{}
	}
	return Uname{
		Release: unix.ByteSliceToString(u.Release[:]),
		Version: unix.ByteSliceToString(u.Version[:]),
		Machine: unix.ByteSliceToString(u.Machine[:]),
	}
}

// landlockABI is the kernel's Landlock ABI version: landlock_create_ruleset with the
// version flag answers it without making a ruleset (0: no Landlock, or seccomp refuses
// the call).
func landlockABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 || int(v) < 0 {
		return 0
	}
	return int(v)
}

// userNamespaces starts `true` in a new user namespace: whether this kernel and the
// container's seccomp profile let an unprivileged process make one (Docker's default
// profile refuses unshare(CLONE_NEWUSER)). nil when there is no `true` to start.
func userNamespaces() *bool {
	bin, err := exec.LookPath("true")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWUSER}
	ok := cmd.Run() == nil
	return &ok
}
