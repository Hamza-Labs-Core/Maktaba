//go:build !darwin && !linux && !windows

package hostinfo

import "time"

// ProcessCPU reads nothing on this OS.
func ProcessCPU([]int) map[int]time.Duration { return map[int]time.Duration{} }
