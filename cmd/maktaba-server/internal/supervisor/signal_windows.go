//go:build windows

package supervisor

import "os"

// terminationSignal: Windows has no SIGTERM, and os/exec can only
// deliver os.Kill to a child reliably. The child's drain logic doesn't
// run, but the supervisor still tears the stack down cleanly.
func terminationSignal() os.Signal { return os.Kill }
