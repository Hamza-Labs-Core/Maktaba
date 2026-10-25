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
			_ = syscall.Kill(-group(c), syscall.SIGTERM)
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
		_ = syscall.Kill(-group(c), syscall.SIGKILL)
	}
}

// group is the process group a listed process leads.
func group(c layout.HandoverChild) int {
	if c.PGID > 0 {
		return c.PGID
	}
	return c.PID
}

// stopTVJob: POSIX has no job; stopListed and the orphans' stop end what was left.
func stopTVJob(string) {}
