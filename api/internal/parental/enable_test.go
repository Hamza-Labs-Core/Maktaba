package parental

import "testing"

// Epic 57 story 57.12 T04: Rate libraries starts a library at All ages when at least
// nine in ten of its classified videos are lectures, and at Not rated otherwise.
func TestSuggestTier(t *testing.T) {
	for _, c := range []struct {
		name string
		in   map[string]int
		all  bool
	}{
		{"95% lectures", map[string]int{"lecture": 95, "film": 5}, true},
		{"50% lectures", map[string]int{"lecture": 50, "film": 50}, false},
		{"exactly 90%", map[string]int{"lecture": 80, "sermon": 10, "interview": 10}, true},
		{"just under 90%", map[string]int{"lecture": 89, "film": 11}, false},
		{"unknown and unclassified do not count", map[string]int{"lecture": 9, "film": 1, "unknown": 500, "": 500}, true},
		{"nothing classified", map[string]int{"unknown": 40, "": 10}, false},
		{"no videos", nil, false},
		{"only films", map[string]int{"film": 3}, false},
	} {
		got := SuggestTier(c.in)
		if c.all != (got != nil && *got == TierAll) {
			t.Errorf("%s: suggested %v, want All ages %v", c.name, got, c.all)
		}
	}
}

// Only a stored `false` is off: a missing, null or unreadable value is on, so a fault
// never loosens a limit.
func TestEnabledValue(t *testing.T) {
	for raw, want := range map[string]bool{"false": false, "true": true, "null": true, `"false"`: true, "0": true, "": true, "{": true} {
		v := nullStr(raw)
		if got := enabledValue(v); got != want {
			t.Errorf("enabledValue(%q) = %v, want %v", raw, got, want)
		}
	}
}
