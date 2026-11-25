package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The guard's time (research P5; the review of #1607): how long a pending release may take
// to confirm, counted while the launcher runs. The machine switched off, asleep, or its
// clock set forward (a NAS with no clock of its own, then NTP) is no time the release had:
// by the wall clock, a good update was given up on at the first look after a long power cut,
// and the home held. A clock set back does not give a hung release more either.
//
//   - Each guard has a budget of running time (layout.GuardBudget: what the release that
//     switched allows its database step and watch), counted across the launcher's starts
//     (guard-ran.json), and never extended: whatever the release being judged pushes, it is
//     given up on there.
//   - Within it, the deadline the release pushes ahead while it starts is read as time left
//     when it is written: a clock set forward after that takes nothing away, and every start
//     of the launcher gives PendingTimeout again, for the release to push.
//   - A release given up on with nothing to go back to stays: its time starts again (again),
//     twice as long each time.

// guardGap is the most a look counts: a longer gap between two looks is sleep (or a
// machine too busy to look), no time the release ran.
const guardGap = time.Minute

// guardSaveEvery is how often the time used is saved.
const guardSaveEvery = 30 * time.Second

// guardClock counts the running time of the pending release.
type guardClock struct {
	run  time.Duration // how long this launcher ran, looks only
	last time.Duration // the tick of the last look

	key   string        // the guard it counts for
	ran   time.Duration // its running time, the starts before included
	saved time.Duration // ran as saved last
	floor time.Duration // run at this start's PendingTimeout
	kept  int           // how often it was given up on with nothing to go back to
	// the deadline as written, and run at it
	deadline time.Time
	end      time.Duration
}

// guardRan is guard-ran.json.
type guardRan struct {
	Guard string  `json:"guard"`
	Sec   float64 `json:"ran_seconds"`
}

func (ln *launcher) guardRanPath() string { return filepath.Join(ln.l.UpdatesDir(), "guard-ran.json") }

// pendingGuard is what the pending release's marker (a layout's) or guard (a bundle's, a
// runtime's) says of its time.
func (ln *launcher) pendingGuard(ver string) (key string, deadline time.Time, budget time.Duration) {
	if ln.cfg.Bundle != "" {
		if g := ln.bundleGuard(); g != nil {
			return g.Version + " " + g.RunID + " " + g.Since.UTC().Format(time.RFC3339Nano), g.Deadline,
				layout.GuardBudget(g.MaxRunSec, g.RollingBack)
		}
	} else if p, err := ln.l.ReadPending(); err == nil && p != nil && p.Version == ver {
		return p.Version + " " + p.RunID + " " + p.Since.UTC().Format(time.RFC3339Nano), p.Deadline,
			layout.GuardBudget(p.MaxRunSec, p.RollingBack)
	}
	return "", time.Time{}, 0
}

// guardLook counts the time since the last look and reports whether the pending release
// ver is to be given up on.
func (ln *launcher) guardLook(ver string) bool {
	key, deadline, budget := ln.pendingGuard(ver)
	if key == "" {
		ln.clock.pass(ln.cfg.Tick())
		return false
	}
	up := ln.clock.look(ln.cfg.Tick(), ln.cfg.Now(), key, deadline, budget, ln.cfg.PendingTimeout, ln.readGuardRan)
	if ln.clock.ran-ln.clock.saved >= guardSaveEvery {
		ln.saveGuardRan()
	}
	return up
}

// pass counts the running time since the last look: a longer gap than guardGap is none of it.
func (c *guardClock) pass(tick time.Duration) time.Duration {
	step := min(max(0, tick-c.last), guardGap)
	c.last = tick
	c.run += step
	return step
}

// look is a look at tick (running time) and now (the clock) at the guard key, which says
// deadline and gives budget; ran reads what the launcher's starts before used of it.
func (c *guardClock) look(tick time.Duration, now time.Time, key string, deadline time.Time, budget, timeout time.Duration, ran func(key string) time.Duration) bool {
	step := c.pass(tick)
	if key != c.key {
		// this launcher's first look at it: what its starts before used, and its grace
		*c = guardClock{run: c.run, last: c.last, key: key, ran: ran(key), floor: c.run + timeout}
		c.saved = c.ran
	}
	c.ran += step
	if deadline.IsZero() { // a switch no update run watches
		return c.ran >= budget || c.run >= c.floor
	}
	left := max(0, min(deadline.Sub(now), budget))
	if !deadline.Equal(c.deadline) {
		c.deadline, c.end = deadline, c.run+left // written since: by this clock
	} else {
		c.end = max(c.end, c.run+left) // the same: a clock set forward takes nothing away
	}
	return c.ran >= budget || c.run >= max(c.floor, c.end)
}

// again starts the time of the guard over: it was given up on, and there is nothing to go
// back to, so its release stays. Without it the time used up ends every start at its first
// look, for good (the review of #1607). Twice as long each time (PendingTimeout, at most the
// guard's whole budget): the library and its recovery page stay up for a person, and a
// release that hung is still started again.
func (c *guardClock) again(timeout time.Duration) {
	c.kept = min(c.kept+1, 7)
	c.ran, c.saved, c.deadline, c.end = 0, 0, time.Time{}, 0
	c.floor = c.run + timeout<<(c.kept-1)
}

// startGuardAgain is again, for the starts after this one too.
func (ln *launcher) startGuardAgain() {
	ln.clock.again(ln.cfg.PendingTimeout)
	_ = os.Remove(ln.guardRanPath())
}

// readGuardRan is the running time the launcher's starts before used of the guard key.
func (ln *launcher) readGuardRan(key string) time.Duration {
	var was guardRan
	b, err := os.ReadFile(ln.guardRanPath())
	if err != nil || json.Unmarshal(b, &was) != nil || was.Guard != key || was.Sec < 0 {
		return 0
	}
	return time.Duration(was.Sec * float64(time.Second))
}

// saveGuardRan saves the running time used (best effort), for the next start to go on from.
func (ln *launcher) saveGuardRan() {
	c := &ln.clock
	if c.key == "" || c.ran == c.saved {
		return
	}
	b, _ := json.Marshal(guardRan{Guard: c.key, Sec: c.ran.Seconds()})
	if err := os.MkdirAll(ln.l.UpdatesDir(), 0o700); err == nil && layout.WriteFileAtomic(ln.guardRanPath(), append(b, '\n'), 0o600) == nil {
		c.saved = c.ran
	}
}

// forgetGuard drops the time counted (the guard is gone).
func (ln *launcher) forgetGuard() {
	ln.clock.key, ln.clock.ran, ln.clock.saved = "", 0, 0
	_ = os.Remove(ln.guardRanPath())
}
