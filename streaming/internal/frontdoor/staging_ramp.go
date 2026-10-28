package frontdoor

import (
	"time"

	"github.com/google/uuid"
)

// The rolling window of a staging run (TV change #55, 2026-10-28). A change of the
// streaming binary changes every channel, and the staging budget is what live and the
// standby leave: on a quiet home that was the whole host (max_active_channels 28), so
// the staging slot started all 28 channels at once. Their output then ran at 0.2-0.7x
// real time (28 1080p encodes, each also decoding, scaling and drawing its strip, each
// asking to catch up at 2x): every station fell steadily further behind the wall clock
// (-40 s at 80 s, -80 s at 100 s) and failed channel.position and channel.wallclock.
// The release was not slower than the one on live; the verification overloaded the
// host. A station that falls behind also never settles, so its rolling probe never ended
// and the load never went down.
//
// So the changed channels are probed in a window that starts at rampStart and grows
// only while every warm staging station is on the wall clock; a station whose output
// runs slower than real time (the host is overloaded) halves it at once, and the window
// never again reaches the size that overloaded the host. A release that cannot keep up
// even alone still fails: the window only sets how many channels are measured at once,
// never what a measurement may show.
//
// A shrink never stops a station under a sample's read (TV change #56: the window went
// from 14 to 6 under a live.ts read, and the broken read failed ts.conformity): the
// stations being read stay warm, draining, until the read ends; the others go at once.
// A station the budget must stop under a read has that sample retried, not scored
// (planStagingWarm, sampleStaging).

const (
	// rampStart is the first window of rolling probes.
	rampStart = 4
	// paceSpan is the stretch of wall clock a station's output rate is measured over.
	paceSpan = 4 * time.Second
	// paceFloor is the output rate (output seconds per second) below which a station
	// is not keeping up: one on the clock runs at 1, one catching up at up to 2.
	paceFloor = 0.85
	// onClock is how far behind the wall clock a station may be and count as on it
	// (channel.position's tolerance).
	onClock = 2 * time.Second
)

// stationPace is what the ramp knows of one staging station's output.
type stationPace struct {
	markReached, markAt time.Time // the start of the current measurement span
	rate                float64   // output seconds per second over the last full span
	measured            bool      // rate covers a full span
	behind              time.Duration
}

// observe records a slot status that reports the station's output at reached as of at.
func (p *stationPace) observe(reached, at time.Time) {
	if reached.IsZero() || at.IsZero() {
		return
	}
	p.behind = at.Sub(reached)
	if p.markAt.IsZero() || at.Before(p.markAt) {
		p.markReached, p.markAt = reached, at
		return
	}
	if span := at.Sub(p.markAt); span >= paceSpan {
		p.rate = reached.Sub(p.markReached).Seconds() / span.Seconds()
		p.measured = true
		p.markReached, p.markAt = reached, at
	}
}

// rampWindow is the next rolling window: halved (and capped below the size that
// overloaded the host) when a warm station's output runs slower than real time,
// doubled when the window is full, channels wait for it and every warm station is
// measured and on the wall clock. rolling is the number of rolling probes warm now;
// ceiling 0 means no overload was seen yet.
func rampWindow(window, ceiling, rolling, budget int, waiting bool, paces []stationPace) (next, nextCeiling int, shrunk bool) {
	if window < 1 {
		window = minInt(rampStart, maxInt(budget, 1))
	}
	slow, allOn := false, len(paces) > 0
	for _, p := range paces {
		if p.measured && p.rate < paceFloor {
			slow = true
		}
		if !p.measured || p.behind > onClock {
			allOn = false
		}
	}
	switch {
	case slow && rolling > 0:
		return maxInt(1, rolling/2), maxInt(1, rolling), true
	case !slow && allOn && waiting && rolling >= window:
		grow := 2 * window
		if ceiling > 0 && grow > ceiling-1 {
			grow = ceiling - 1
		}
		if grow > budget {
			grow = budget
		}
		if grow > window {
			window = grow
		}
	}
	return window, ceiling, false
}

// rampLocked measures the warm staging stations from the staging slot's last status
// and moves the rolling window (s.mu held).
func (s *Slots) rampLocked(run *stagingRun, staging string, budget int) {
	if run.pace == nil {
		run.pace = map[uuid.UUID]*stationPace{}
	}
	h := s.health[staging]
	reached := map[string]time.Time{}
	var at time.Time
	if h != nil && h.ok {
		at = h.at
		for _, c := range h.status.Channels {
			if c.Reached != nil {
				reached[c.ID] = *c.Reached
			}
		}
	}
	var paces []stationPace
	rolling := 0
	for _, w := range run.warm {
		id, err := uuid.Parse(w.Channel)
		if err != nil {
			continue
		}
		if w.Why == WhyRolling {
			rolling++
		}
		p := run.pace[id]
		if p == nil {
			p = &stationPace{}
			run.pace[id] = p
		}
		if r, ok := reached[w.Channel]; ok && at.After(p.markAt) {
			p.observe(r, at)
		}
		paces = append(paces, *p)
	}
	was := run.window
	next, ceiling, shrunk := rampWindow(run.window, run.ceiling, rolling, budget, len(run.deferred) > 0, paces)
	run.window, run.ceiling = next, ceiling
	if shrunk {
		// the stations left measure their rate afresh under the lighter load
		run.pace = map[uuid.UUID]*stationPace{}
	}
	if was != 0 && next != was {
		s.log().Info("tv: staging rolling window", "change_id", run.plan.ChangeID, "window", next, "was", was,
			"ceiling", ceiling, "overloaded", shrunk, "event", "tv_staging_window")
	}
}
