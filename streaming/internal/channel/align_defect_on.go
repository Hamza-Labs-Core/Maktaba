//go:build tvdefect

package channel

import (
	"strconv"
	"strings"
)

// A tvdefect build (the staging verification e2e test's broken releases)
// applies BuildDefect.
func init() { applyBuildDefect(BuildDefect) }

func applyBuildDefect(d string) {
	switch {
	case d == "pids":
		TSPMTPID, TSVideoPID, TSAudioPID = 0x1100, 0x200, 0x201
	case strings.HasPrefix(d, "pts:"):
		if ms, err := strconv.Atoi(strings.TrimPrefix(d, "pts:")); err == nil {
			ptsShiftTicks = int64(ms) * 90
		}
	}
}
