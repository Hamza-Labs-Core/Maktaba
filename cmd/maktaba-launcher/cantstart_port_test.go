package main

import "testing"

// Epic 59 story 59.08: the can't-start page takes the web port the container sets.
func TestPageWebPort(t *testing.T) {
	for v, want := range map[string]string{"": "8088", "18088": "18088", " 9088 ": "9088", "abc": "8088", "0": "8088", "70000": "8088"} {
		t.Setenv("MAKTABA_WEB_PORT", v)
		if got := pageWebPort(); got != want {
			t.Errorf("MAKTABA_WEB_PORT=%q: %s, want %s", v, got, want)
		}
	}
}
