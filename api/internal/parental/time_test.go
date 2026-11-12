package parental_test

// 57.14 T01: the decision alone, over rule sets and clock times, with slots that
// cross midnight and the 04:00 day line. 10 November 2026 is a Tuesday.

import (
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

const (
	tLibA = "00000000-0000-4000-8000-0000005714a0"
	tLibB = "00000000-0000-4000-8000-0000005714b0"

	everyDay = 127
	tuesday  = 1 << 2
)

func homeZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func limit(days, minutes int, lib string) parental.TimeRule {
	return parental.TimeRule{Days: days, DailyMinutes: &minutes, LibraryID: lib}
}

func slot(days int, from, to string) parental.TimeRule {
	return parental.TimeRule{Days: days, SlotStart: from, SlotEnd: to}
}

func TestEvaluateTime(t *testing.T) {
	loc := homeZone(t)
	type want struct {
		allowed   bool
		limited   bool
		remaining time.Duration
		reason    string
		ends      string // EndsAt, "" for none
		slotEnds  string
		next      string
	}
	cases := []struct {
		name  string
		rules []parental.TimeRule
		now   string
		lib   string
		used  map[string]int
		grant int
		want  want
	}{
		{name: "no rules: no limit and no end", now: "2026-11-10 19:00:00", want: want{allowed: true}},
		{name: "a daily limit, nothing watched",
			rules: []parental.TimeRule{limit(everyDay, 60, "")}, now: "2026-11-10 19:00:00",
			want: want{allowed: true, limited: true, remaining: time.Hour, ends: "2026-11-10 20:00:00"}},
		{name: "a daily limit, part watched in two libraries",
			rules: []parental.TimeRule{limit(everyDay, 60, "")}, now: "2026-11-10 19:00:00",
			used: map[string]int{tLibA: 600, tLibB: 1200},
			want: want{allowed: true, limited: true, remaining: 30 * time.Minute, ends: "2026-11-10 19:30:00"}},
		{name: "the limit is reached",
			rules: []parental.TimeRule{limit(everyDay, 60, "")}, now: "2026-11-10 19:00:00",
			used: map[string]int{tLibA: 3600},
			want: want{limited: true, reason: parental.ReasonLimitReached, next: "2026-11-11 04:00:00"}},
		{name: "a limit of zero minutes never opens",
			rules: []parental.TimeRule{limit(everyDay, 0, "")}, now: "2026-11-10 19:00:00",
			want: want{limited: true, reason: parental.ReasonLimitReached}},
		{name: "inside a slot, thirty seconds before it closes",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00")}, now: "2026-11-10 19:59:30",
			want: want{allowed: true, limited: true, remaining: 30 * time.Second, ends: "2026-11-10 20:00:00", slotEnds: "2026-11-10 20:00:00"}},
		{name: "the slot closes on the minute",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00")}, now: "2026-11-10 20:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-11 07:00:00"}},
		{name: "before the slot opens, after the day line",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00")}, now: "2026-11-10 06:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 07:00:00"}},
		{name: "before the slot opens, before the day line",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00")}, now: "2026-11-10 03:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 07:00:00"}},
		{name: "a slot across midnight, before midnight",
			rules: []parental.TimeRule{slot(tuesday, "21:00", "01:00")}, now: "2026-11-10 23:00:00",
			want: want{allowed: true, limited: true, remaining: 2 * time.Hour, ends: "2026-11-11 01:00:00", slotEnds: "2026-11-11 01:00:00"}},
		{name: "a slot across midnight, after midnight: still Tuesday's home day",
			rules: []parental.TimeRule{slot(tuesday, "21:00", "01:00")}, now: "2026-11-11 00:30:00",
			want: want{allowed: true, limited: true, remaining: 30 * time.Minute, ends: "2026-11-11 01:00:00", slotEnds: "2026-11-11 01:00:00"}},
		{name: "a slot across midnight, after it closed: open again when Wednesday starts",
			rules: []parental.TimeRule{slot(tuesday, "21:00", "01:00")}, now: "2026-11-11 01:30:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-11 04:00:00"}},
		{name: "a slot across midnight, Tuesday afternoon",
			rules: []parental.TimeRule{slot(tuesday, "21:00", "01:00")}, now: "2026-11-10 15:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 21:00:00"}},
		{name: "Monday night under a Tuesday rule: free until Tuesday's day line, where the rule closes it",
			rules: []parental.TimeRule{slot(tuesday, "21:00", "01:00")}, now: "2026-11-09 23:00:00",
			want: want{allowed: true, remaining: 5 * time.Hour, ends: "2026-11-10 04:00:00"}},
		{name: "the day line: one minute before 04:00 the reached limit still holds",
			rules: []parental.TimeRule{limit(everyDay, 60, "")}, now: "2026-11-11 03:59:00",
			used: map[string]int{tLibA: 3600},
			want: want{limited: true, reason: parental.ReasonLimitReached, next: "2026-11-11 04:00:00"}},
		{name: "the day line: playing across 04:00 goes on into the new day's limit",
			rules: []parental.TimeRule{limit(everyDay, 60, "")}, now: "2026-11-11 03:59:30",
			used: map[string]int{tLibA: 3540},
			want: want{allowed: true, limited: true, remaining: 60*time.Minute + 30*time.Second, ends: "2026-11-11 05:00:00"}},
		{name: "the strictest wins: the slot closes before the limit runs out",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00"), limit(everyDay, 60, "")}, now: "2026-11-10 19:30:00",
			want: want{allowed: true, limited: true, remaining: 30 * time.Minute, ends: "2026-11-10 20:00:00", slotEnds: "2026-11-10 20:00:00"}},
		{name: "the strictest wins: the limit runs out before the slot closes",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00"), limit(everyDay, 60, "")}, now: "2026-11-10 12:00:00",
			used: map[string]int{tLibA: 3000},
			want: want{allowed: true, limited: true, remaining: 10 * time.Minute, ends: "2026-11-10 12:10:00", slotEnds: "2026-11-10 20:00:00"}},
		{name: "outside the slot is said before the reached limit",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00"), limit(everyDay, 60, "")}, now: "2026-11-10 21:00:00",
			used: map[string]int{tLibA: 3600},
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-11 07:00:00"}},
		{name: "two slots on one day leave only what both allow",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00"), slot(everyDay, "18:00", "22:00")}, now: "2026-11-10 17:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 18:00:00"}},
		{name: "a library rule of 30 inside an account rule of 120: that library stops at 30",
			rules: []parental.TimeRule{limit(everyDay, 120, ""), limit(everyDay, 30, tLibA)}, now: "2026-11-10 12:00:00",
			lib: tLibA, used: map[string]int{tLibA: 1800},
			want: want{limited: true, reason: parental.ReasonLimitReached, next: "2026-11-11 04:00:00"}},
		{name: "a library rule of 30 inside an account rule of 120: the others go on to 120",
			rules: []parental.TimeRule{limit(everyDay, 120, ""), limit(everyDay, 30, tLibA)}, now: "2026-11-10 12:00:00",
			lib: tLibB, used: map[string]int{tLibA: 1800},
			want: want{allowed: true, limited: true, remaining: 90 * time.Minute, ends: "2026-11-10 13:30:00"}},
		{name: "a library rule of 30 inside an account rule of 120: the account's limit closes every library",
			rules: []parental.TimeRule{limit(everyDay, 120, ""), limit(everyDay, 30, tLibA)}, now: "2026-11-10 12:00:00",
			lib: tLibB, used: map[string]int{tLibA: 1800, tLibB: 5400},
			want: want{limited: true, reason: parental.ReasonLimitReached, next: "2026-11-11 04:00:00"}},
		{name: "a library rule alone does not touch another library",
			rules: []parental.TimeRule{limit(everyDay, 30, tLibA)}, now: "2026-11-10 12:00:00",
			lib: tLibB, used: map[string]int{tLibA: 1800},
			want: want{allowed: true}},
		{name: "a grant after the limit: allowed for the granted minutes",
			rules: []parental.TimeRule{limit(everyDay, 60, "")}, now: "2026-11-10 19:00:00",
			used: map[string]int{tLibA: 3600}, grant: 1800,
			want: want{allowed: true, limited: true, remaining: 30 * time.Minute, ends: "2026-11-10 19:30:00"}},
		{name: "a grant opens a closed slot until it runs out",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00")}, now: "2026-11-10 20:30:00", grant: 600,
			want: want{allowed: true, limited: true, remaining: 10 * time.Minute, ends: "2026-11-10 20:40:00"}},
		{name: "a grant adds to what the limit still leaves",
			rules: []parental.TimeRule{limit(everyDay, 60, "")}, now: "2026-11-10 19:00:00",
			used: map[string]int{tLibA: 3000}, grant: 900,
			want: want{allowed: true, limited: true, remaining: 25 * time.Minute, ends: "2026-11-10 19:25:00"}},
		{name: "a grant does not reach past the day line into a closed day",
			rules: []parental.TimeRule{slot(everyDay, "07:00", "20:00")}, now: "2026-11-11 03:50:00", grant: 3600,
			want: want{allowed: true, limited: true, remaining: 10 * time.Minute, ends: "2026-11-11 04:00:00"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := at(t, loc, c.now)
			got := parental.EvaluateTime(c.rules, loc, now, c.lib, c.used, c.grant)
			stamp := func(p *time.Time) string {
				if p == nil {
					return ""
				}
				return p.In(loc).Format("2006-01-02 15:04:05")
			}
			if got.Allowed != c.want.allowed || got.Limited != c.want.limited || got.Remaining != c.want.remaining || got.Reason != c.want.reason {
				t.Errorf("allowed %v limited %v remaining %v reason %q, want %v %v %v %q",
					got.Allowed, got.Limited, got.Remaining, got.Reason, c.want.allowed, c.want.limited, c.want.remaining, c.want.reason)
			}
			if stamp(got.EndsAt) != c.want.ends || stamp(got.SlotEndsAt) != c.want.slotEnds || stamp(got.NextAllowedAt) != c.want.next {
				t.Errorf("ends %q slot ends %q next %q, want %q %q %q",
					stamp(got.EndsAt), stamp(got.SlotEndsAt), stamp(got.NextAllowedAt), c.want.ends, c.want.slotEnds, c.want.next)
			}
		})
	}
}

// The home's zone decides, not the instant's own: one moment is inside the slot in
// one home and outside it in another.
func TestEvaluateTimeReadsTheHomeZone(t *testing.T) {
	riyadh := homeZone(t)
	rules := []parental.TimeRule{slot(everyDay, "07:00", "20:00")}
	now := at(t, riyadh, "2026-11-10 19:30:00") // 16:30 UTC
	if st := parental.EvaluateTime(rules, riyadh, now, "", nil, 0); !st.Allowed {
		t.Fatalf("19:30 in the home's zone: %+v", st)
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	if st := parental.EvaluateTime(rules, tokyo, now, "", nil, 0); st.Allowed || st.Reason != parental.ReasonOutsideSlot {
		t.Fatalf("the same moment is 01:30 in Tokyo: %+v", st)
	}
}

func TestHomeDay(t *testing.T) {
	loc := homeZone(t)
	for now, day := range map[string]string{
		"2026-11-10 04:00:00": "2026-11-10",
		"2026-11-10 23:59:59": "2026-11-10",
		"2026-11-11 00:30:00": "2026-11-10", // a late evening is one day
		"2026-11-11 03:59:59": "2026-11-10",
		"2026-11-11 04:00:00": "2026-11-11",
		"2026-01-01 02:00:00": "2025-12-31",
	} {
		if got := parental.DayKey(at(t, loc, now), loc); got != day {
			t.Errorf("DayKey(%s) = %s, want %s", now, got, day)
		}
	}
}
