package updates

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// StartMargin is how long before the window's end a run may still start (story 35.3
// AC5: "at least 30 minutes before its end").
const StartMargin = 30 * time.Minute

// Window is a daily quiet window in local wall time, "HH:MM-HH:MM". The end may be
// earlier than the start: the window crosses midnight ("23:00-01:00", story 35.3 EC1).
type Window struct {
	StartMin, EndMin int // minutes after local midnight
}

// ParseWindow reads "HH:MM-HH:MM". An empty or zero-length window is refused.
func ParseWindow(s string) (Window, error) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return Window{}, fmt.Errorf("%w: %q (want HH:MM-HH:MM)", ErrBadWindow, s)
	}
	sm, err1 := hhmm(a)
	em, err2 := hhmm(b)
	if err1 != nil || err2 != nil {
		return Window{}, fmt.Errorf("%w: %q (want HH:MM-HH:MM)", ErrBadWindow, s)
	}
	if sm == em {
		return Window{}, fmt.Errorf("%w: %q starts and ends at the same time", ErrBadWindow, s)
	}
	if (em-sm+1440)%1440 <= int(StartMargin/time.Minute) {
		return Window{}, fmt.Errorf("%w: %q is not longer than %d minutes (a run starts at least that long before the end)",
			ErrBadWindow, s, int(StartMargin/time.Minute))
	}
	return Window{StartMin: sm, EndMin: em}, nil
}

func hhmm(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) == 0 || len(h) > 2 || len(m) != 2 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	hn, err := strconv.Atoi(h)
	if err != nil || hn < 0 || hn > 23 {
		return 0, fmt.Errorf("bad hour %q", s)
	}
	mn, err := strconv.Atoi(m)
	if err != nil || mn < 0 || mn > 59 {
		return 0, fmt.Errorf("bad minute %q", s)
	}
	return hn*60 + mn, nil
}

// String is the window as "HH:MM-HH:MM".
func (w Window) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.StartMin/60, w.StartMin%60, w.EndMin/60, w.EndMin%60)
}

// Occurrence is the window's run that starts on the local calendar day of day.
//
// Wall time is what the window means (story 35.3 EC2): the start and end are local
// clock readings on their days. A start inside a skipped hour (spring forward) is the
// moment the clocks jump, so that night's window is shorter by the skipped hour; on the
// fall-back night a repeated hour makes it longer (an ambiguous reading is its first
// occurrence).
func (w Window) Occurrence(day time.Time, loc *time.Location) (start, end time.Time) {
	d := day.In(loc)
	y, m, dd := d.Date()
	start = wallTime(y, m, dd, w.StartMin, loc)
	endDay := dd
	if w.EndMin <= w.StartMin {
		endDay++ // crosses midnight
	}
	end = wallTime(y, m, endDay, w.EndMin, loc)
	return start, end
}

// wallTime is the instant a local clock reads mins minutes after midnight of the day. A
// reading inside a skipped hour (spring forward) does not exist: it is the instant the
// clocks jump, so a window starting or ending in the gap loses the skipped part.
func wallTime(y int, m time.Month, d, mins int, loc *time.Location) time.Time {
	t := time.Date(y, m, d, mins/60, mins%60, 0, 0, loc)
	if t.Hour() == mins/60 && t.Minute() == mins%60 {
		return t
	}
	// in a gap: the transition is the zone boundary nearest t
	zs, ze := t.ZoneBounds()
	if !zs.IsZero() && t.Sub(zs) < 3*time.Hour && t.Sub(zs) >= -3*time.Hour {
		return zs
	}
	if !ze.IsZero() {
		return ze
	}
	return t
}

// Containing is the occurrence that contains t, if any.
func (w Window) Containing(t time.Time, loc *time.Location) (start, end time.Time, ok bool) {
	local := t.In(loc)
	for _, off := range []int{-1, 0} {
		s, e := w.Occurrence(local.AddDate(0, 0, off), loc)
		if !t.Before(s) && t.Before(e) {
			return s, e, true
		}
	}
	return time.Time{}, time.Time{}, false
}

// CanStart reports whether a run may start at t: inside an occurrence and at least
// StartMargin before its end.
func (w Window) CanStart(t time.Time, loc *time.Location) bool {
	_, e, ok := w.Containing(t, loc)
	return ok && !t.After(e.Add(-StartMargin))
}

// NextStart is the first instant at or after from at which a run may start (from itself
// when it can start then), searching up to a year ahead; zero when none (cannot happen
// for a valid window longer than the margin).
func (w Window) NextStart(from time.Time, loc *time.Location) time.Time {
	if w.CanStart(from, loc) {
		return from
	}
	local := from.In(loc)
	for off := -1; off <= 370; off++ {
		s, e := w.Occurrence(local.AddDate(0, 0, off), loc)
		latest := e.Add(-StartMargin)
		if latest.Before(s) || latest.Before(from) {
			continue
		}
		if s.Before(from) {
			return from
		}
		return s
	}
	return time.Time{}
}
