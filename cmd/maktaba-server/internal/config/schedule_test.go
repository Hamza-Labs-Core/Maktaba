package config

import "testing"

// Epic 59 story 59.02: quiet hours parse as HH:MM-HH:MM, across midnight too; "" is none;
// [schedule] quiet_hours set wins over the tier's, "" there meaning none.
func TestQuietHours(t *testing.T) {
	for _, ok := range []string{"01:00-06:00", "22:00-06:00", " 00:30 - 05:15 "} {
		if _, _, none, err := ParseQuietHours(ok); err != nil || none {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"1-6", "01:00", "24:00-06:00", "01:00-01:00", "01:0-06:00", "aa:bb-cc:dd"} {
		if _, _, _, err := ParseQuietHours(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, _, none, err := ParseQuietHours(""); !none || err != nil {
		t.Error("empty is none")
	}
	c := Defaults()
	if got := QuietHoursOf(c, "01:00-06:00"); got != "01:00-06:00" {
		t.Errorf("the tier's: %q", got)
	}
	empty, late := "", "22:00-06:00"
	c.Schedule.QuietHours = &empty
	if got := QuietHoursOf(c, "01:00-06:00"); got != "" {
		t.Errorf("set to none: %q", got)
	}
	c.Schedule.QuietHours = &late
	if got := QuietHoursOf(c, ""); got != late {
		t.Errorf("set on Strong: %q", got)
	}
}
