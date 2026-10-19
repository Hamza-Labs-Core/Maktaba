package updates

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tz data for %s: %v", name, err)
	}
	return loc
}

func at(loc *time.Location, y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, loc)
}

func TestParseWindow(t *testing.T) {
	for _, ok := range []string{"03:00-05:00", "23:00-01:00", "0:00-1:00", " 22:30 - 23:59 "} {
		if _, err := ParseWindow(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "03:00", "3-5", "24:00-01:00", "03:60-05:00", "03:00-03:00", "03:00-03:30", "23:50-00:10", "a:00-b:00"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TC2 (story 35.3): the window's edges, the 30-minute margin, a window across midnight,
// DST changes in two time zones.
func TestSchedulerWindow(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	lon := mustLoc(t, "Europe/London")
	tokyo := mustLoc(t, "Asia/Tokyo")
	night := Window{StartMin: 3 * 60, EndMin: 5 * 60}
	cross := Window{StartMin: 23 * 60, EndMin: 1 * 60}
	cases := []struct {
		name string
		w    Window
		t    time.Time
		loc  *time.Location
		want bool
	}{
		{"before the window", night, at(ny, 2026, 10, 20, 2, 59), ny, false},
		{"at the start", night, at(ny, 2026, 10, 20, 3, 0), ny, true},
		{"inside", night, at(ny, 2026, 10, 20, 4, 0), ny, true},
		{"exactly 30 min before the end", night, at(ny, 2026, 10, 20, 4, 30), ny, true},
		{"inside the last 30 minutes", night, at(ny, 2026, 10, 20, 4, 31), ny, false},
		{"after the end", night, at(ny, 2026, 10, 20, 5, 0), ny, false},
		{"local time, not UTC", night, time.Date(2026, 10, 20, 3, 30, 0, 0, time.UTC), ny, false},
		{"same instant in Tokyo", night, time.Date(2026, 10, 19, 18, 30, 0, 0, time.UTC), tokyo, true},
		{"across midnight, before midnight", cross, at(lon, 2026, 10, 20, 23, 15), lon, true},
		{"across midnight, after midnight", cross, at(lon, 2026, 10, 21, 0, 20), lon, true},
		{"across midnight, last 30 minutes", cross, at(lon, 2026, 10, 21, 0, 45), lon, false},
		{"across midnight, before the start", cross, at(lon, 2026, 10, 20, 22, 59), lon, false},
		// New York springs forward 2026-03-08 02:00 -> 03:00: 02:00-04:00 is one hour long
		{"NY spring forward: 03:30 EDT is inside 02:00-04:00 and before the margin", Window{120, 240}, at(ny, 2026, 3, 8, 3, 30), ny, true},
		{"NY spring forward: 03:31 EDT is in the margin", Window{120, 240}, at(ny, 2026, 3, 8, 3, 31), ny, false},
		// New York falls back 2026-11-01 02:00 -> 01:00: 01:00-03:00 is three hours long
		{"NY fall back: 01:30 EST (second pass) is inside 01:00-03:00", Window{60, 180}, time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), ny, true},
		{"NY fall back: 02:30 EST is inside the margin", Window{60, 180}, at(ny, 2026, 11, 1, 2, 31), ny, false},
		// London springs forward 2026-03-29 01:00 -> 02:00, falls back 2026-10-25 02:00 -> 01:00
		{"London spring forward: 01:00-03:00 starts at 02:00 BST", Window{60, 180}, at(lon, 2026, 3, 29, 2, 0), lon, true},
		{"London spring forward: 02:31 BST in the margin", Window{60, 180}, at(lon, 2026, 3, 29, 2, 31), lon, false},
		{"London fall back: 03:00-05:00 normal", night, at(lon, 2026, 10, 25, 4, 0), lon, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.w.CanStart(c.t, c.loc); got != c.want {
				s, e := c.w.Occurrence(c.t, c.loc)
				t.Fatalf("CanStart(%s) = %v, want %v (occurrence %s - %s)", c.t.In(c.loc), got, c.want, s, e)
			}
		})
	}
	// EC2: the skipped hour shortens that night's window; the repeated one lengthens it
	s, e := Window{120, 240}.Occurrence(at(ny, 2026, 3, 8, 12, 0), ny)
	if d := e.Sub(s); d != time.Hour {
		t.Errorf("spring-forward night lasts %s, want 1h", d)
	}
	s, e = Window{60, 180}.Occurrence(at(ny, 2026, 11, 1, 12, 0), ny)
	if d := e.Sub(s); d != 3*time.Hour {
		t.Errorf("fall-back night lasts %s, want 3h", d)
	}
}

func TestWindowNextStart(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	w := Window{StartMin: 3 * 60, EndMin: 5 * 60}
	cases := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"evening: tonight's start", at(ny, 2026, 10, 20, 21, 0), at(ny, 2026, 10, 21, 3, 0)},
		{"inside: now", at(ny, 2026, 10, 21, 3, 10), at(ny, 2026, 10, 21, 3, 10)},
		{"in the margin: tomorrow", at(ny, 2026, 10, 21, 4, 45), at(ny, 2026, 10, 22, 3, 0)},
		{"after: tomorrow", at(ny, 2026, 10, 21, 6, 0), at(ny, 2026, 10, 22, 3, 0)},
	}
	for _, c := range cases {
		if got := w.NextStart(c.from, ny); !got.Equal(c.want) {
			t.Errorf("%s: NextStart(%s) = %s, want %s", c.name, c.from, got.In(ny), c.want)
		}
	}
	cross := Window{StartMin: 23 * 60, EndMin: 60}
	if got, want := cross.NextStart(at(ny, 2026, 10, 21, 0, 10), ny), at(ny, 2026, 10, 21, 0, 10); !got.Equal(want) {
		t.Errorf("across midnight inside: %s, want %s", got, want)
	}
	if got, want := cross.NextStart(at(ny, 2026, 10, 21, 0, 40), ny), at(ny, 2026, 10, 21, 23, 0); !got.Equal(want) {
		t.Errorf("across midnight margin: %s, want %s", got, want)
	}
}

// Release age, deferral and the window together (story 35.3 AC5; the plan's next run).
func TestMakePlanAgeDeferralWindow(t *testing.T) {
	enableAutoInstall(t)
	ny := mustLoc(t, "America/New_York")
	pol := DefaultPolicy()
	published := at(ny, 2026, 10, 20, 12, 0)
	cat := []Release{rel("1.5.0", func(r *Release) { r.Manifest.Published = published })}

	// the night after publishing is too early (24 h); the next night is the run
	now := at(ny, 2026, 10, 21, 3, 5)
	pl := MakePlan(now, ny, pol, cat, "1.4.0", "")
	if pl.Due || pl.Waiting != WaitReleaseAge || pl.NextRunAt == nil || !pl.NextRunAt.Equal(at(ny, 2026, 10, 22, 3, 0)) {
		t.Fatalf("young release: %+v next=%v", pl, pl.NextRunAt)
	}
	now = at(ny, 2026, 10, 22, 3, 5)
	if pl = MakePlan(now, ny, pol, cat, "1.4.0", ""); !pl.Due {
		t.Fatalf("old enough inside the window: %+v", pl)
	}
	// 0 = as soon as seen
	p0 := pol
	p0.MinReleaseAgeHours = 0
	if pl = MakePlan(at(ny, 2026, 10, 21, 3, 5), ny, p0, cat, "1.4.0", ""); !pl.Due {
		t.Fatalf("age 0: %+v", pl)
	}
	// outside the window
	if pl = MakePlan(at(ny, 2026, 10, 22, 12, 0), ny, pol, cat, "1.4.0", ""); pl.Due || pl.Waiting != WaitWindow {
		t.Fatalf("midday: %+v", pl)
	}
	// deferred past tonight
	pd := pol
	until := at(ny, 2026, 10, 24, 0, 0)
	pd.DeferredUntil = &until
	pl = MakePlan(at(ny, 2026, 10, 22, 3, 5), ny, pd, cat, "1.4.0", "")
	if pl.Due || pl.Waiting != WaitDeferred || !pl.NextRunAt.Equal(at(ny, 2026, 10, 24, 3, 0)) {
		t.Fatalf("deferred: %+v next=%v", pl, pl.NextRunAt)
	}
	// deferred until inside a window: that instant
	until = at(ny, 2026, 10, 22, 3, 20)
	if pl = MakePlan(at(ny, 2026, 10, 22, 3, 5), ny, pd, cat, "1.4.0", ""); pl.Waiting != WaitDeferred || !pl.NextRunAt.Equal(until) {
		t.Fatalf("deferred into the window: %+v next=%v", pl, pl.NextRunAt)
	}
	// auto_install off: check and notify only (AC9)
	off := pol
	off.AutoInstall = false
	if pl = MakePlan(at(ny, 2026, 10, 22, 3, 5), ny, off, cat, "1.4.0", ""); pl.Due || pl.NextRunAt != nil || pl.Waiting != WaitAutoInstallOff {
		t.Fatalf("auto_install off: %+v", pl)
	}
	// contract hold
	held := []Release{rel("2.0.0", contract)}
	if pl = MakePlan(at(ny, 2026, 10, 22, 3, 5), ny, pol, held, "1.4.0", ""); pl.Due || !pl.Held || pl.Waiting != ReasonHeld {
		t.Fatalf("held: %+v", pl)
	}
}

func TestMakePlanPhaseA(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	cat := []Release{rel("1.5.0")}
	pl := MakePlan(at(ny, 2026, 10, 22, 3, 5), ny, DefaultPolicy(), cat, "1.4.0", "")
	if pl.Due || pl.Waiting != WaitNotBuilt || pl.Target == nil {
		t.Fatalf("phase A must not install by itself: %+v", pl)
	}
}
