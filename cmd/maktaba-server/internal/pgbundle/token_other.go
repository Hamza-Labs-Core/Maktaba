//go:build !windows

package pgbundle

import "os/exec"

// restrict is Windows' (token_windows.go): elsewhere postgres refuses root on
// its own and the server never runs as root.
func restrict(*exec.Cmd) error { return nil }
