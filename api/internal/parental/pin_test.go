package parental

import (
	"testing"
	"time"
)

// 57.06 T04: the lock on a guardian's PIN is 15 minutes, then twice as long each
// time in a row, and never more than 24 hours.
func TestLockForDoublesTo24Hours(t *testing.T) {
	want := []time.Duration{
		15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour,
		8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour,
	}
	for i, w := range want {
		if got := lockFor(i + 1); got != w {
			t.Errorf("lock %d = %v, want %v", i+1, got, w)
		}
	}
	if got := lockFor(0); got != 15*time.Minute {
		t.Errorf("lockFor(0) = %v", got)
	}
	if got := lockFor(1000); got != 24*time.Hour {
		t.Errorf("lockFor(1000) = %v", got)
	}
}

func TestChildLockedUntil(t *testing.T) {
	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	every := func(n int, step time.Duration) []time.Time {
		out := make([]time.Time, n)
		for i := range out {
			out[i] = t0.Add(time.Duration(i) * step)
		}
		return out
	}
	if got := childLockedUntil(every(9, time.Minute), 10, time.Hour, time.Hour); !got.IsZero() {
		t.Errorf("nine wrong tries closed the account until %v", got)
	}
	if got := childLockedUntil(every(10, time.Minute), 10, time.Hour, time.Hour); !got.Equal(t0.Add(9*time.Minute + time.Hour)) {
		t.Errorf("ten in nine minutes: closed until %v", got)
	}
	// ten spread over more than an hour never close it
	if got := childLockedUntil(every(10, 7*time.Minute), 10, time.Hour, time.Hour); !got.IsZero() {
		t.Errorf("ten in 63 minutes closed the account until %v", got)
	}
	// the hold runs from the last try that was inside a full window
	if got := childLockedUntil(every(12, time.Minute), 10, time.Hour, time.Hour); !got.Equal(t0.Add(11*time.Minute + time.Hour)) {
		t.Errorf("twelve in eleven minutes: closed until %v", got)
	}
	if got := childLockedUntil(nil, 10, time.Hour, time.Hour); !got.IsZero() {
		t.Errorf("no tries: %v", got)
	}
}

func TestValidPIN(t *testing.T) {
	for pin, want := range map[string]bool{
		"482915": true, "000000": true,
		"": false, "48291": false, "4829155": false, "48291a": false, "48 915": false, "-48291": false,
		"١٢٣٤٥٦": false, "１２３４５６": false,
	} {
		if got := ValidPIN(pin); got != want {
			t.Errorf("ValidPIN(%q) = %v, want %v", pin, got, want)
		}
	}
}

// The family's day ends at 04:00 home time.
func TestDayEndAndHomeDay(t *testing.T) {
	riyadh, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Skip(err)
	}
	for _, c := range []struct{ now, end, day string }{
		{"2026-11-11T12:00:00Z", "2026-11-12T01:00:00Z", "2026-11-11"}, // 15:00
		{"2026-11-11T23:30:00Z", "2026-11-12T01:00:00Z", "2026-11-11"}, // 02:30 the next morning
		{"2026-11-12T01:00:00Z", "2026-11-13T01:00:00Z", "2026-11-12"}, // 04:00 exactly: the new day
		{"2026-11-12T00:59:59Z", "2026-11-12T01:00:00Z", "2026-11-11"},
	} {
		now, _ := time.Parse(time.RFC3339, c.now)
		if got := DayEnd(now, riyadh).UTC().Format(time.RFC3339); got != c.end {
			t.Errorf("DayEnd(%s) = %s, want %s", c.now, got, c.end)
		}
		if got := HomeDay(now, riyadh); got != c.day {
			t.Errorf("HomeDay(%s) = %s, want %s", c.now, got, c.day)
		}
	}
}

func TestActivityCursor(t *testing.T) {
	at := time.Date(2026, 11, 11, 12, 0, 0, 123456000, time.UTC)
	id := "00000000-0000-4000-8000-000057080001"
	gotAt, gotID, err := decodeCursor(encodeCursor(at, id))
	if err != nil || !gotAt.Equal(at) || gotID != id {
		t.Errorf("round trip = %v %s %v", gotAt, gotID, err)
	}
	for _, bad := range []string{"nonsense", "!!", "", encodeCursor(at, "not-an-id"), "MjAyNnxub3BlfHg"} {
		if _, _, err := decodeCursor(bad); err != ErrBadCursor {
			t.Errorf("decodeCursor(%q) = %v, want ErrBadCursor", bad, err)
		}
	}
}

func TestKinds(t *testing.T) {
	for _, k := range []string{"watched", "blocked", "asked", "approved", "denied", "pin_locked", "signal"} {
		if !ValidEventKind(k) {
			t.Errorf("event kind %q is not valid", k)
		}
	}
	if ValidEventKind("") || ValidEventKind("nonsense") {
		t.Error("an unknown event kind is valid")
	}
	for _, k := range []string{"library", "collection", "series", "channel", "youtube_source", "plugin", "video", "time"} {
		if !ValidRequestKind(k) {
			t.Errorf("request kind %q is not valid", k)
		}
	}
	if ValidRequestKind("minutes") || ValidRequestKind("") {
		t.Error("an unknown request kind is valid")
	}
	// more time is asked for, never named by a rule
	if ValidSubjectKind(RequestTime) {
		t.Error("a rule can name `time`")
	}
	for id, want := range map[string]int{"1": 1, "30": 30, "480": 480} {
		if n, ok := RequestMinutes(id); !ok || n != want {
			t.Errorf("RequestMinutes(%q) = %d, %v", id, n, ok)
		}
	}
	for _, id := range []string{"", "0", "481", "-1", "030", "+30", "3.5", "abc", " 30", "30 "} {
		if _, ok := RequestMinutes(id); ok {
			t.Errorf("RequestMinutes(%q) is valid", id)
		}
	}
}
