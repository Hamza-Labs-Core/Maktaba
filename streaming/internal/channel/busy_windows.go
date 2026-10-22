//go:build windows

package channel

import (
	"errors"
	"syscall"
)

// isBusy: err is a sharing violation (another process holds the file without sharing
// it: ReplaceFileW, a rename of the node config).
func isBusy(err error) bool { return errors.Is(err, syscall.Errno(32)) } // ERROR_SHARING_VIOLATION
