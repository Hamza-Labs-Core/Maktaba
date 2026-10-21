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

// Detail is what a process waits on before its start, or why its start step
// failed: Code, from a fixed list, and Params for a screen to say it in its own
// language, and Text, the English, for the logs (ChildStatus's detail fields).
// Params is not changed once the Detail is reported.
type Detail struct {
	Code   string
	Params map[string]any
	Text   string
}

// Coded is an error whose detail is d (a Prepare's failure for a screen).
func Coded(d Detail) error { return &detailError{d: d} }

type detailError struct{ d Detail }

func (e *detailError) Error() string { return e.d.Text }

// DetailOf is the Detail err carries (through Halt too), else its text alone.
func DetailOf(err error) Detail {
	var de *detailError
	if errors.As(err, &de) {
		return de.d
	}
	return Detail{Text: err.Error()}
}

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
	report := func(d Detail) {
		s.mu.Lock()
		c.status.Detail, c.status.DetailCode, c.status.DetailParams = d.Text, d.Code, d.Params
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
			report(Detail{})
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
		d := DetailOf(err)
		s.mu.Lock()
		c.status.State = StateFailed
		c.status.Detail, c.status.DetailCode, c.status.DetailParams = d.Text, d.Code, d.Params
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
