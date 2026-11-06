package logbench

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"strconv"
	"time"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// referenceWork and referenceLines are how long the two calibration loops take on the
// reference machine (an Apple M2, the Mac class of docs/performance/logs.md): a hashing loop,
// and the keeper's own kind of work (decode, scrub and encode log lines), which a runner's
// SHA instructions say nothing about.
const (
	referenceWork  = 60 * time.Millisecond
	referenceLines = 44 * time.Millisecond
)

// linesWork decodes, scrubs and encodes 5,000 corpus lines (the best of three rounds).
func linesWork() time.Duration {
	lines := Corpus(9, 5000, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC), time.Hour)
	best := time.Duration(1<<63 - 1)
	for round := 0; round < 3; round++ {
		start := time.Now()
		for _, l := range lines {
			var m map[string]any
			_ = json.Unmarshal(l, &m)
			for k, v := range m {
				if s, ok := v.(string); ok {
					m[k] = mlog.Scrub(s)
				}
			}
			_, _ = json.Marshal(m)
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

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
	if g := float64(linesWork()) / float64(referenceLines); g > f {
		f = g
	}
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
