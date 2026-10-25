//go:build !windows

package main

import (
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// orphanStopGrace is how long the processes a record lists have to stop after SIGTERM
// before their groups are killed.
var orphanStopGrace = 10 * time.Second

// stopListed stops the process groups a handover record lists, each only while its
// leader is still the process the record names (never one that reuses its pid):
// SIGTERM, then SIGKILL after grace.
func stopListed(rec *layout.Handover, grace time.Duration) {
	var left []layout.HandoverChild
	for _, c := range rec.Children {
		if layout.ProcessIs(c.PID, c.Start) {
			left = append(left, c)
			signalListed(c, syscall.SIGTERM)
		}
	}
	deadline := time.Now().Add(grace)
	for len(left) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		kept := left[:0]
		for _, c := range left {
			if layout.ProcessIs(c.PID, c.Start) {
				kept = append(kept, c)
			}
		}
		left = kept
	}
	for _, c := range left {
		signalListed(c, syscall.SIGKILL)
	}
}

// signalListed signals a listed process: its process group when the record names one
// (the group it leads, with whatever it started), else the process itself (one that
// leads no group: a signal to -pid would reach nobody).
func signalListed(c layout.HandoverChild, sig syscall.Signal) {
	if c.PGID > 0 && syscall.Kill(-c.PGID, sig) == nil {
		return
	}
	_ = syscall.Kill(c.PID, sig)
}

// stopTVJob: POSIX has no job; stopListed and the orphans' stop end what was left.
func stopTVJob(string) {}
