package supervisor

import (
	"context"
	"time"
)

// prepareRetryFirst is the wait after a Prepare's first failure; it doubles up to
// the policy's failed_retry. A Prepare's work (a venv build, a download) fails for
// reasons that do not pass in a second (the network, a full disk).
var prepareRetryFirst = 30 * time.Second

// prepare runs c's Prepare until it succeeds (Epic 38 story 38.12). Meanwhile c
// is starting, with what the Prepare reports as its detail; after a failure it is
// failed, with the error as its detail, until the next try. It returns false when
// the supervisor stops first.
func (s *Supervisor) prepare(ctx context.Context, c *child) bool {
	delay := prepareRetryFirst
	if fr := s.policy.FailedRetry; fr > 0 && delay > fr {
		delay = fr
	}
	report := func(detail string) {
		s.mu.Lock()
		c.status.Detail = detail
		s.mu.Unlock()
	}
	for {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			return false
		}
		c.status.State = StateStarting
		c.status.NextRestartAt = nil
		s.mu.Unlock()
		err := c.proc.Prepare(ctx, report)
		if err == nil {
			report("")
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		next := s.now().Add(delay)
		s.mu.Lock()
		c.status.State = StateFailed
		c.status.Detail = err.Error()
		c.status.NextRestartAt = &next
		s.mu.Unlock()
		s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventFailed, Reason: ReasonStartFailed,
			DelaySec: delay.Seconds(), Message: err.Error()})
		s.logf("%s cannot start yet: %v; trying again in %s", c.proc.Name(), err, delay)
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
		delay *= 2
		if delay > s.policy.FailedRetry {
			delay = s.policy.FailedRetry
		}
	}
}
