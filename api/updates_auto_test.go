package main

import (
	"context"
	"testing"
)

// A home with its updates turned off says so on /api/cluster/self (updates_auto false):
// the Linux app then activates a newer payload itself.
func TestHomeAutoUpdatesOff(t *testing.T) {
	t.Setenv("MAKTABA_UPDATE_DISABLE", "1")
	if homeAutoUpdates(nil)(context.Background()) {
		t.Fatal("updates disabled, said on")
	}
}
