//go:build linux

package hostload

import (
	"os"
	"strconv"
	"strings"
)

func loadAvg() ([3]float64, bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return [3]float64{}, false
	}
	return parseLoadAvg(string(b))
}

// parseLoadAvg reads the first three fields of /proc/loadavg.
func parseLoadAvg(s string) ([3]float64, bool) {
	var out [3]float64
	f := strings.Fields(s)
	if len(f) < 3 {
		return out, false
	}
	for i := range out {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return out, false
		}
		out[i] = v
	}
	return out, true
}
