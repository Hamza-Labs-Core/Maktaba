package releaseverify

import (
	"testing"
	"time"
)

// The one "never go back" rule: a newer version signed before the installed release on
// its major.minor line is an older build; a higher major.minor never is (a hotfix line
// moving up); unknown times decide nothing.
func TestOlderBuild(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	v030, rc7, v031 := at("2026-10-24T05:44:07Z"), at("2026-10-24T06:17:37Z"), at("2026-10-24T17:57:06Z")
	for _, c := range []struct {
		cand      string
		candAt    time.Time
		installed string
		instAt    time.Time
		want      bool
	}{
		{"0.3.0", v030, "0.3.0-rc.7", rc7, true},            // rc.7 tagged after 0.3.0
		{"v0.3.0", v030, "v0.3.0-rc.7", rc7, true},          // with the tag's v
		{"0.3.1", v031, "0.3.0-rc.7", rc7, false},           // newer and later: a move up
		{"0.3.0", v030, "0.2.5", rc7, false},                // a hotfix line moving up (22.5 EC1)
		{"1.0.0", v030, "0.3.0-rc.7", rc7, false},           // another major
		{"0.3.0-rc.7", rc7, "0.3.0", v030, false},           // not newer: the downgrade rule's
		{"0.3.0", v030, "0.3.0", rc7, false},                // the same version: the replay rule's
		{"0.3.0", time.Time{}, "0.3.0-rc.7", rc7, false},    // unknown time
		{"0.3.0", v030, "0.3.0-rc.7", time.Time{}, false},   // unknown time
		{"0.3.2", v030, "0.3.1", rc7, true},                 // out-of-order tags on a line
		{"0.3.0-rc.8", v030, "0.3.0-rc.7", rc7, true},       // an rc built before
		{"0.3.0-1-gabc1234", v030, "0.3.0-rc.7", rc7, true}, // a describe build of the release
	} {
		if got := OlderBuild(c.cand, c.candAt, c.installed, c.instAt); got != c.want {
			t.Errorf("OlderBuild(%s, %s) = %v, want %v", c.cand, c.installed, got, c.want)
		}
	}
	if !SameLine("1.5.0", "1.5.2-rc.1") || SameLine("0.2.5", "0.3.0") || SameLine("x", "1.5.0") {
		t.Error("SameLine")
	}
}
