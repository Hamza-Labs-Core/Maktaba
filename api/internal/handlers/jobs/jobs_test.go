package jobs

import "testing"

func TestIsTerminal(t *testing.T) {
	cases := map[string]bool{
		"done":       true,
		"failed":     true,
		"cancelled":  true,
		"superseded": true,
		"running":    false,
		"pending":    false,
		"paused":     false,
	}
	for s, want := range cases {
		if got := isTerminal(s); got != want {
			t.Errorf("isTerminal(%q) = %v want %v", s, got, want)
		}
	}
}
