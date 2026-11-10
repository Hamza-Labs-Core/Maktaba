package main

import "testing"

func TestSSDPOn(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{"": true, "1": true, "on": true, "0": false, "false": false, " OFF ": false} {
		if got := ssdpOn(func(string) string { return v }); got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
}
