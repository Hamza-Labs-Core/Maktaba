package supervisor

import (
	"context"
	"errors"
	"time"
)

// prepareRetryFirst is the wait after a Prepare's first failure; it doubles up to
// the policy's failed_retry. A Prepare's work (a venv build, a download) fails for
// reasons that do not pass in a second (the network, a full disk).
var prepareRetryFirst = 30 * time.Second

// Halt marks a Prepare's error as one no retry in this run can mend (Epic 38
// story 38.12: an update whose migrations failed and whose database was put
// back, a release older than the database): the process stays failed, with
// the error as its detail and no next try, until the server starts again.
func Halt(err error) error {
	if err == nil {
		return nil
	}
	return &haltError{err: err}
}

type haltError struct{ err error }

func (e *haltError) Error() string { return e.err.Error() }
func (e *haltError) Unwrap() error { return e.err }

// Halted reports whether err is (or wraps) a Halt.
func Halted(err error) bool {
	var h *haltError
	return errors.As(err, &h)
}

// prepare runs c's Prepare until it succeeds (Epic 38 story 38.12). Meanwhile c
// is starting, with what the Prepare reports as its detail; after a failure it is
// failed, with the error as its detail, until the next try (none after a Halt:
// it stays failed until the supervisor stops). It returns false when the
// supervisor stops first.
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
		halted := Halted(err)
		var next *time.Time
		if !halted {
			at := s.now().Add(delay)
			next = &at
		}
		s.mu.Lock()
		c.status.State = StateFailed
		c.status.Detail = err.Error()
		c.status.NextRestartAt = next
		s.mu.Unlock()
		ev := Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventFailed, Reason: ReasonStartFailed, Message: err.Error()}
		if halted {
			s.emit(ev)
			s.logf("%s cannot start: %v; it stays stopped until the server starts again", c.proc.Name(), err)
			<-ctx.Done()
			return false
		}
		ev.DelaySec = delay.Seconds()
		s.emit(ev)
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
