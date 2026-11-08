package statements

import "testing"

func TestSettingsInScope(t *testing.T) {
	tv := []string{"planner", "tv"}
	for _, c := range []struct {
		key      string
		sections []string
		want     bool
	}{
		{"rules.tv", tv, true},
		{"rules_version.planner", tv, true},
		// the tv rules still read the dizqueTV export's live list (slot 0215 keeps the row)
		{"rules.dizquetv", tv, true},
		{"rules.dizquetv", []string{"planner"}, false},
		{"rules_version.dizquetv", tv, false},
		{"rules.detect", tv, false},
		{"plugins.tv.maktaba.livetv", tv, false},
		{"", tv, false},
		{"rules.tv", nil, false},
	} {
		if got := settingsInScope(c.key, c.sections); got != c.want {
			t.Errorf("settingsInScope(%q, %v) = %v, want %v", c.key, c.sections, got, c.want)
		}
	}
}
