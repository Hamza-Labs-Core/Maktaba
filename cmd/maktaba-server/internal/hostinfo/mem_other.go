//go:build !darwin && !linux && !windows

package hostinfo

// FreeMemory is 0: not read on this OS (nothing is held back for memory).
func FreeMemory() uint64 { return 0 }

// ProcessRSS reads nothing on this OS.
func ProcessRSS([]int) map[int]uint64 { return map[int]uint64{} }
