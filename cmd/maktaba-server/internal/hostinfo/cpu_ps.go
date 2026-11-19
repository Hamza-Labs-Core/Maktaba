package hostinfo

import (
	"strconv"
	"strings"
	"time"
)

// parsePSTime reads ps's time forms: [[dd-]hh:]mm:ss[.ff].
func parsePSTime(s string) (time.Duration, bool) {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = n, rest
	}
	f := strings.Split(s, ":")
	if len(f) < 2 || len(f) > 3 {
		return 0, false
	}
	sec, err := strconv.ParseFloat(f[len(f)-1], 64)
	if err != nil || sec < 0 || sec >= 60 {
		return 0, false
	}
	total := time.Duration(sec * float64(time.Second))
	units := []time.Duration{time.Minute, time.Hour}
	for i := len(f) - 2; i >= 0; i-- {
		n, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		total += time.Duration(n) * units[len(f)-2-i]
	}
	return total + time.Duration(days)*24*time.Hour, true
}
