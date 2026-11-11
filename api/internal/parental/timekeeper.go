package parental

// The time keeper (57.14): the one place the API asks whether a restricted account
// may play now, counts what it watched, and tells streaming when a running stream
// must end.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// TimePusher tells streaming when playback of one account in one library must end
// (libraryID "": playback with no library). A nil endAt clears it: nothing ends the
// stream. Streaming refuses that account's segments from endAt on.
type TimePusher interface {
	SetTimeLimit(ctx context.Context, userID, libraryID string, endAt *time.Time, reason string, nextAllowedAt *time.Time) error
}

type sessionMark struct {
	userID    string
	libraryID string
	at        time.Time
	pos       float64
	carry     time.Duration
}

// TimeKeeper answers and counts for the API. The zero value is not usable: use
// NewTimeKeeper.
type TimeKeeper struct {
	cache *Cache
	store *Store
	// Push is where end times go (nil: no streaming service to tell).
	Push TimePusher

	mu    sync.Mutex
	marks map[string]*sessionMark // by stream session id
}

func NewTimeKeeper(cache *Cache, push TimePusher) *TimeKeeper {
	return &TimeKeeper{cache: cache, store: cache.Store(), Push: push, marks: map[string]*sessionMark{}}
}

// Time answers whether p may play in libraryID now ("": the account alone), for
// how long, and why not. An admin or an unrestricted account is allowed at once,
// with no time query. An error means the answer is not known: the caller refuses.
func (k *TimeKeeper) Time(ctx context.Context, p *principal.Principal, libraryID string) (TimeState, error) {
	if p == nil {
		return TimeState{}, errors.New("parental time: no principal")
	}
	if p.IsAdmin {
		return TimeState{Allowed: true}, nil
	}
	eff, err := k.cache.For(ctx, p)
	if err != nil {
		return TimeState{}, err
	}
	if eff == nil || !eff.Restricted {
		return TimeState{Allowed: true}, nil
	}
	return k.store.TimeOf(ctx, p.UserID, libraryID)
}

// Admit is Time for a stream about to start or renew: when it is allowed for a
// restricted account, streaming is told when it must end before Admit returns. If
// streaming cannot be told, Admit returns the error and the stream is refused.
func (k *TimeKeeper) Admit(ctx context.Context, p *principal.Principal, libraryID string) (TimeState, error) {
	st, err := k.Time(ctx, p, libraryID)
	if err != nil || st.Now.IsZero() {
		return st, err // st.Now is zero for an admin or an unrestricted account
	}
	if !st.Allowed {
		_ = k.push(ctx, p.UserID, libraryID, st) // a stream still running ends now
		return st, nil
	}
	if err := k.push(ctx, p.UserID, libraryID, st); err != nil {
		return TimeState{}, err
	}
	return st, nil
}

func (k *TimeKeeper) push(ctx context.Context, userID, libraryID string, st TimeState) error {
	if k.Push == nil {
		return nil
	}
	if !st.Allowed {
		now := st.Now
		return k.Push.SetTimeLimit(ctx, userID, libraryID, &now, st.Reason, st.NextAllowedAt)
	}
	reason := ""
	if st.EndsAt != nil {
		reason = ReasonLimitReached
		if st.SlotEndsAt != nil && !st.SlotEndsAt.After(*st.EndsAt) {
			reason = ReasonOutsideSlot
		}
	}
	return k.Push.SetTimeLimit(ctx, userID, libraryID, st.EndsAt, reason, nil)
}

// marksKept bounds the session marks; a mark not posted to for markIdle is dropped.
const (
	marksKept = 4096
	markIdle  = 6 * time.Hour
)

// Progress counts one progress post of a stream session (the stream's heartbeat).
// It adds the time since the session's last post, never more than HeartbeatCap, and
// only when the position changed: a paused player adds nothing. Then streaming is
// told the new end time. An unrestricted account costs no query.
func (k *TimeKeeper) Progress(ctx context.Context, userID, sessionID, videoID string, positionSec float64) error {
	eff, err := k.cache.Effective(ctx, userID)
	if err != nil {
		return err
	}
	if eff == nil || !eff.Restricted {
		k.mu.Lock()
		delete(k.marks, sessionID)
		k.mu.Unlock()
		return nil
	}
	now := k.store.now()
	k.mu.Lock()
	m := k.marks[sessionID]
	k.mu.Unlock()
	if m == nil || m.userID != userID {
		var lib sql.NullString
		err := k.store.DB.QueryRowContext(ctx, `SELECT library_id::text FROM videos WHERE id = $1`, videoID).Scan(&lib)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		m = &sessionMark{userID: userID, libraryID: lib.String, at: now, pos: positionSec}
		k.mu.Lock()
		if len(k.marks) >= marksKept {
			for id, old := range k.marks {
				if now.Sub(old.at) > markIdle {
					delete(k.marks, id)
				}
			}
		}
		if len(k.marks) < marksKept {
			k.marks[sessionID] = m
		}
		k.mu.Unlock()
		return k.Refresh(ctx, userID, m.libraryID)
	}
	k.mu.Lock()
	gap := now.Sub(m.at)
	moved := math.Abs(positionSec-m.pos) >= 0.5
	seconds := 0
	if gap > 0 {
		m.at = now
	}
	if moved {
		m.pos = positionSec
		if gap > 0 {
			credit := min(gap, HeartbeatCap) + m.carry
			seconds = int(credit / time.Second)
			m.carry = credit - time.Duration(seconds)*time.Second
		}
	}
	lib := m.libraryID
	k.mu.Unlock()
	if seconds > 0 {
		if err := k.store.AddUsage(ctx, userID, lib, seconds); err != nil {
			return fmt.Errorf("parental time: count: %w", err)
		}
	}
	return k.Refresh(ctx, userID, lib)
}

// Refresh tells streaming again when userID's playback must end, after the count,
// the rules or a grant changed: for the libraries named, and for every library a
// session of that account is known to be playing in.
func (k *TimeKeeper) Refresh(ctx context.Context, userID string, libraryIDs ...string) error {
	if k.Push == nil {
		return nil
	}
	libs := map[string]bool{}
	for _, l := range libraryIDs {
		libs[l] = true
	}
	k.mu.Lock()
	for _, m := range k.marks {
		if m.userID == userID {
			libs[m.libraryID] = true
		}
	}
	k.mu.Unlock()
	if len(libs) == 0 {
		return nil
	}
	eff, err := k.cache.Effective(ctx, userID)
	if err != nil {
		return err
	}
	var first error
	for lib := range libs {
		var err error
		if eff == nil || !eff.Restricted {
			err = k.Push.SetTimeLimit(ctx, userID, lib, nil, "", nil)
		} else {
			var st TimeState
			if st, err = k.store.TimeOf(ctx, userID, lib); err == nil {
				err = k.push(ctx, userID, lib, st)
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
