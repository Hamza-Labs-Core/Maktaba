//go:build !windows

package channel

import "os"

// openShared opens a file for reading (POSIX already allows rename/unlink
// of open files).
func openShared(path string) (*os.File, error) { return os.Open(path) }
