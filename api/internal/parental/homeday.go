package parental

import (
	"context"
	"time"
)

// DayLineHour is when a day ends for the family: 04:00, home time. A PIN unlock
// lasts until then, and a blocked item is noted once per such day.
const DayLineHour = 4

// HomeLocation is the home's time zone: app_settings `home.timezone` (an IANA
// name), or the server's zone when it is unset or unknown. A device's clock is
// never read.
func (s *Store) HomeLocation(ctx context.Context) *time.Location {
	var name string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(value #>> '{}', '') FROM app_settings WHERE key = 'home.timezone'`).Scan(&name); err != nil || name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

// DayEnd is the next day line after now in loc.
func DayEnd(now time.Time, loc *time.Location) time.Time {
	n := now.In(loc)
	end := time.Date(n.Year(), n.Month(), n.Day(), DayLineHour, 0, 0, 0, loc)
	if !end.After(n) {
		end = time.Date(n.Year(), n.Month(), n.Day()+1, DayLineHour, 0, 0, 0, loc)
	}
	return end
}

// HomeDay names the family's day now falls in ("2026-11-11"): the date at the
// last day line.
func HomeDay(now time.Time, loc *time.Location) string {
	return now.In(loc).Add(-DayLineHour * time.Hour).Format("2006-01-02")
}

// Clock is the store's time now.
func (s *Store) Clock() time.Time { return s.now() }
