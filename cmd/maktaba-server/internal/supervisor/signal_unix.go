//go:build !windows

package supervisor

import (
	"os"
	"syscall"
)

// terminationSignal is the graceful-stop signal sent to children on
// shutdown. On Unix that's SIGTERM, which every service's
// signal.NotifyContext already traps to begin its drain.
func terminationSignal() os.Signal { return syscall.SIGTERM }
