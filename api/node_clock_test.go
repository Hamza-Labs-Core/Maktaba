package main

import (
	"path/filepath"
	"testing"
	"time"
)

// The home's clock as last measured outlives a restart of this API (an outage that
// began before it): the copy's beat is still read on the home's clock.
func TestTheHomesClockOutlivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node", "home-clock.json")
	saveClock(path, savedClock{OffsetNS: int64(-90 * time.Second), At: time.Now().UTC()})
	var c homeClock
	c.keepAt(path)
	if got := c.Offset(); got != -90*time.Second {
		t.Fatalf("the kept offset: %v", got)
	}
	// none kept: none
	var fresh homeClock
	fresh.keepAt(filepath.Join(t.TempDir(), "none.json"))
	if got := fresh.Offset(); got != 0 {
		t.Fatalf("no kept offset: %v", got)
	}
}
