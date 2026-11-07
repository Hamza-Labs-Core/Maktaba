//go:build !darwin && !linux

package hostinfo

// LoadAverage is false: Windows (and the rest) keep no load average.
func LoadAverage() (float64, bool) { return 0, false }
