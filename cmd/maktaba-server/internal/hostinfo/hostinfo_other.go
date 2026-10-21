//go:build !darwin && !linux && !windows

package hostinfo

import "errors"

// TotalMemory is 0: not read on this OS.
func TotalMemory() uint64 { return 0 }

func freeDisk(string) (uint64, error) { return 0, errors.New("not supported on this OS") }
