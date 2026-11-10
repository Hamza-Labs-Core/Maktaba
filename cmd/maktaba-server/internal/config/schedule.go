package config

// Quiet hours (Epic 59 story 59.02): a window of the day, HH:MM-HH:MM in the home's time
// zone, when heavy work runs. A window may cross midnight (22:00-06:00).

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseQuietHours reads "HH:MM-HH:MM" into minutes of the day; "" is no window (ok, and
// none). Start and end the same is refused (a window of nothing, or all day).
func ParseQuietHours(s string) (start, end int, none bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, true, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, false, fmt.Errorf("quiet hours %q: write them as 01:00-06:00", s)
	}
	if start, err = clock(a); err == nil {
		end, err = clock(b)
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("quiet hours %q: %v", s, err)
	}
	if start == end {
		return 0, 0, false, fmt.Errorf("quiet hours %q: the window starts and ends at the same time", s)
	}
	return start, end, false, nil
}

func clock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 || len(m) != 2 {
		return 0, fmt.Errorf("%q is not a time of day (HH:MM)", s)
	}
	return hh*60 + mm, nil
}

// QuietHoursOf is the window in force: [schedule] quiet_hours when server.toml sets it,
// else tierDefault (the hardware tier's). A window that does not parse is none.
func QuietHoursOf(c Config, tierDefault string) string {
	w := tierDefault
	if c.Schedule.QuietHours != nil {
		w = *c.Schedule.QuietHours
	}
	if _, _, none, err := ParseQuietHours(w); none || err != nil {
		return ""
	}
	return strings.TrimSpace(w)
}
