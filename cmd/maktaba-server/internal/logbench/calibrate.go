package logbench

import (
	"crypto/sha256"
	"os"
	"strconv"
	"time"
)

// referenceWork is how long the calibration loop takes on the reference machine (an Apple
// M2, the Mac class of docs/performance/logs.md).
const referenceWork = 60 * time.Millisecond

// Calibrate is how much slower this machine is than the reference (at least 1, at most 4):
// a budget is multiplied by it, so a slower runner is not failed for being slow, and a 2x
// slower capture still fails on any runner.
func Calibrate() float64 {
	buf := make([]byte, 1<<20)
	best := time.Duration(1<<63 - 1)
	for round := 0; round < 3; round++ {
		start := time.Now()
		var sum [32]byte
		for i := 0; i < 24; i++ {
			sum = sha256.Sum256(append(buf[:len(buf)-32], sum[:]...))
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	f := float64(best) / float64(referenceWork)
	if f < 1 {
		f = 1
	}
	if f > 4 {
		f = 4
	}
	return f
}

// Scale is LOGBENCH_SCALE (1: the CI loads; 6: the story's full burst and corpus).
func Scale() float64 {
	if v, err := strconv.ParseFloat(os.Getenv("LOGBENCH_SCALE"), 64); err == nil && v > 0 {
		return v
	}
	return 1
}
