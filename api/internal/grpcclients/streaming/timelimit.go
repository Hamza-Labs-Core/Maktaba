package streaming

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Epic 57 story 57.14: what the API tells the streaming service of a restricted
// account's playback time, and what it asks back. Must match
// streaming/internal/grpcsrv/serve.go (SetTimeLimit, TimeServed, SetTimeKept).
const (
	methodSetTimeLimit = "/" + streamingService + "/SetTimeLimit"
	methodTimeServed   = "/" + streamingService + "/TimeServed"
	methodSetTimeKept  = "/" + streamingService + "/SetTimeKept"
	// timeLimitTimeout bounds one call: a stream waits on it, and a call that does
	// not answer refuses the stream.
	timeLimitTimeout = 3 * time.Second
)

// TimeLimit is one push: the account's end in a library and, with SessionID, that
// stream session's lease.
type TimeLimit struct {
	EndAt         *time.Time
	Reason        string
	NextAllowedAt *time.Time
	SessionID     string
	LeaseUntil    *time.Time
}

// TimeLimits is the part of the streaming service the time keeper talks to. It is
// its own interface so the many fakes of Client need not grow; the real client
// implements both.
type TimeLimits interface {
	// SetTimeLimit sets what streaming holds for the account in the library; a nil
	// limit removes it.
	SetTimeLimit(ctx context.Context, userID, libraryID string, lim *TimeLimit) error
	// TimeServed is when streaming last served that session to the account; known
	// is false when it holds no lease for the session.
	TimeServed(ctx context.Context, userID, sessionID string) (at time.Time, known bool, err error)
	// ClearTimeLimits drops the end streaming holds for the account in every
	// library (it has no time rule left); its leases stay.
	ClearTimeLimits(ctx context.Context, userID string) error
	// SetTimeKept tells streaming the account's time is kept from now on (a URL
	// signed before that is refused), or no longer is (everything held for it goes).
	SetTimeKept(ctx context.Context, userID string, kept bool) error
}

// confirmed reads the ok of an answer: one that does not say ok was not done (an
// older streaming service answers an unknown method with a transport error, caught
// by the caller; this is for anything in between).
func confirmed(resp map[string]any, what string) error {
	if ok, _ := resp["ok"].(bool); !ok {
		return errors.New("streaming: " + what + " was not confirmed")
	}
	return nil
}

func (c *realClient) ClearTimeLimits(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("streaming: ClearTimeLimits needs a user")
	}
	ctx, cancel := context.WithTimeout(ctx, timeLimitTimeout)
	defer cancel()
	resp := map[string]any{}
	if err := c.invoke(ctx, methodSetTimeLimit, map[string]any{"user_id": userID, "clear_all": true}, &resp); err != nil {
		return err
	}
	return confirmed(resp, "the time limits' clearing")
}

func (c *realClient) SetTimeKept(ctx context.Context, userID string, kept bool) error {
	if userID == "" {
		return errors.New("streaming: SetTimeKept needs a user")
	}
	ctx, cancel := context.WithTimeout(ctx, timeLimitTimeout)
	defer cancel()
	resp := map[string]any{}
	if err := c.invoke(ctx, methodSetTimeKept, map[string]any{"user_id": userID, "kept": kept}, &resp); err != nil {
		return err
	}
	return confirmed(resp, "whether the account's time is kept")
}

func (c *realClient) SetTimeLimit(ctx context.Context, userID, libraryID string, lim *TimeLimit) error {
	if userID == "" {
		return errors.New("streaming: SetTimeLimit needs a user")
	}
	ctx, cancel := context.WithTimeout(ctx, timeLimitTimeout)
	defer cancel()
	body := map[string]any{"user_id": userID, "library_id": libraryID}
	if lim == nil {
		body["clear"] = true
	} else {
		body["reason"] = lim.Reason
		for key, at := range map[string]*time.Time{"end_at": lim.EndAt, "next_allowed_at": lim.NextAllowedAt, "lease_until": lim.LeaseUntil} {
			if at != nil {
				body[key] = at.UTC().Format(time.RFC3339Nano)
			}
		}
		if lim.SessionID != "" {
			body["session_id"] = lim.SessionID
		}
	}
	resp := map[string]any{}
	if err := c.invoke(ctx, methodSetTimeLimit, body, &resp); err != nil {
		return err
	}
	return confirmed(resp, "the time limit")
}

func (c *realClient) TimeServed(ctx context.Context, userID, sessionID string) (time.Time, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeLimitTimeout)
	defer cancel()
	resp := map[string]any{}
	if err := c.invoke(ctx, methodTimeServed, map[string]any{"user_id": userID, "session_id": sessionID}, &resp); err != nil {
		return time.Time{}, false, err
	}
	known, isBool := resp["known"].(bool)
	if !isBool {
		return time.Time{}, false, errors.New("streaming: TimeServed did not say whether the session is known")
	}
	if !known {
		return time.Time{}, false, nil
	}
	s, _ := resp["served_at"].(string)
	if s == "" {
		return time.Time{}, true, nil
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("streaming: served_at: %w", err)
	}
	return at, true, nil
}
