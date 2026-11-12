package parental_test

import (
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// A slot belongs to the home day it starts on. The part of a slot that runs past
// the 04:00 day line is open on the next home day only as the rest of a slot that
// started the day before: a rule that did not cover that day opens nothing.
// 10 November 2026 is a Tuesday.
func TestSlotTailBelongsToItsHomeDay(t *testing.T) {
	loc := homeZone(t)
	const tueWed = tuesday | tuesday<<1
	type want struct {
		allowed, limited bool
		remaining        time.Duration
		reason           string
		ends, slotEnds   string
		next             string
	}
	night := func(days int) []parental.TimeRule { return []parental.TimeRule{slot(days, "22:00", "06:00")} }
	cases := []struct {
		name  string
		rules []parental.TimeRule
		now   string
		want  want
	}{
		{name: "Tuesday only, Tuesday 05:00: no slot started on Monday, so its tail is not open",
			rules: night(tuesday), now: "2026-11-10 05:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 22:00:00"}},
		{name: "Tuesday only, one minute before Tuesday's day line: Monday has no rule",
			rules: night(tuesday), now: "2026-11-10 03:59:00",
			want: want{allowed: true, remaining: time.Minute, ends: "2026-11-10 04:00:00"}},
		{name: "Tuesday only, on the day line: Tuesday starts closed",
			rules: night(tuesday), now: "2026-11-10 04:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 22:00:00"}},
		{name: "Tuesday only, Tuesday 23:00: inside the slot, which closes at 06:00 on Wednesday",
			rules: night(tuesday), now: "2026-11-10 23:00:00",
			want: want{allowed: true, limited: true, remaining: 29 * time.Hour, ends: "2026-11-12 04:00:00", slotEnds: "2026-11-11 06:00:00"}},
		{name: "Tuesday and Wednesday, Wednesday 05:00: the rest of Tuesday's slot",
			rules: night(tueWed), now: "2026-11-11 05:00:00",
			want: want{allowed: true, limited: true, remaining: time.Hour, ends: "2026-11-11 06:00:00", slotEnds: "2026-11-11 06:00:00"}},
		{name: "Tuesday and Wednesday, Wednesday on the day line: still Tuesday's slot",
			rules: night(tueWed), now: "2026-11-11 04:00:00",
			want: want{allowed: true, limited: true, remaining: 2 * time.Hour, ends: "2026-11-11 06:00:00", slotEnds: "2026-11-11 06:00:00"}},
		{name: "Tuesday and Wednesday, Wednesday 06:00: Tuesday's slot closed",
			rules: night(tueWed), now: "2026-11-11 06:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-11 22:00:00"}},
		{name: "Tuesday and Wednesday, Tuesday 05:00: Monday started no slot",
			rules: night(tueWed), now: "2026-11-10 05:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 22:00:00"}},
		{name: "every day, 05:00: the rest of last night's slot, as before",
			rules: night(everyDay), now: "2026-11-10 05:00:00",
			want: want{allowed: true, limited: true, remaining: time.Hour, ends: "2026-11-10 06:00:00", slotEnds: "2026-11-10 06:00:00"}},
		{name: "a slot over the day line and not over midnight, Tuesday 04:30: it starts at 03:00 on Wednesday",
			rules: []parental.TimeRule{slot(tuesday, "03:00", "05:00")}, now: "2026-11-10 04:30:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-11 03:00:00"}},
		{name: "a slot over the day line and not over midnight, Wednesday 03:30: Tuesday's slot",
			rules: []parental.TimeRule{slot(tuesday, "03:00", "05:00")}, now: "2026-11-11 03:30:00",
			want: want{allowed: true, limited: true, remaining: 24*time.Hour + 30*time.Minute, ends: "2026-11-12 04:00:00", slotEnds: "2026-11-11 05:00:00"}},
		{name: "a slot that ends on the day line, an hour before it",
			rules: []parental.TimeRule{slot(everyDay, "20:00", "04:00")}, now: "2026-11-10 03:00:00",
			want: want{allowed: true, limited: true, remaining: time.Hour, ends: "2026-11-10 04:00:00", slotEnds: "2026-11-10 04:00:00"}},
		{name: "a slot that ends on the day line, on it",
			rules: []parental.TimeRule{slot(everyDay, "20:00", "04:00")}, now: "2026-11-10 04:00:00",
			want: want{limited: true, reason: parental.ReasonOutsideSlot, next: "2026-11-10 20:00:00"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parental.EvaluateTime(c.rules, loc, at(t, loc, c.now), "", nil, 0)
			stamp := func(p *time.Time) string {
				if p == nil {
					return ""
				}
				return p.In(loc).Format("2006-01-02 15:04:05")
			}
			have := want{got.Allowed, got.Limited, got.Remaining, got.Reason, stamp(got.EndsAt), stamp(got.SlotEndsAt), stamp(got.NextAllowedAt)}
			if have != c.want {
				t.Errorf("got  %+v\nwant %+v", have, c.want)
			}
		})
	}
}
