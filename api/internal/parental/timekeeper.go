package parental

// The time keeper (57.14): the one place the API asks whether a restricted account
// may play now, counts what it watched, and tells streaming when a running stream
// must end.
//
// The count is the home's own. A stream session is marked when the API opens it,
// and every call the API gets for it (open, renew, progress post, close) adds the
// time since the one before, never more than HeartbeatCap. What the client says its
// position is plays no part. Streaming serves a restricted account's session only
// for HeartbeatCap after the last counted call for that session (its lease), so a
// client that stops calling is stopped, and what it was served before that is
// counted from what streaming says it served. A session whose mark the keeper let
// go has its lease ended first: it is served again only through the API.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// ReasonNoHeartbeat is the reason streaming gives when it does not serve a
// restricted account's session because no counted call for that session came within
// HeartbeatCap. A progress
// post or a renewed session opens it again.
const ReasonNoHeartbeat = "no_heartbeat"

// ErrUnknownSession: the stream session is not one the home opened for that account.
var ErrUnknownSession = errors.New("parental time: not a stream session of that account")

// TimeLimit is what streaming holds for one account in one library.
type TimeLimit struct {
	// EndAt: no media is served from here on (nil: no end).
	EndAt *time.Time
	// SessionID and VideoID name the stream session LeaseUntil is for.
	SessionID, VideoID string
	// LeaseUntil: that session's media (and, for direct play, that video's) is
	// served up to here and no further until a newer lease comes. Nil leaves every
	// lease as it is. A session with no lease is served nothing.
	LeaseUntil *time.Time
	// Reason and NextAllowedAt are what streaming answers once EndAt has passed.
	Reason        string
	NextAllowedAt *time.Time
}

// TimePusher is the streaming service, as the time keeper needs it.
type TimePusher interface {
	// SetTimeLimit sets what streaming holds for the account in the library
	// (libraryID "": playback with no library). A nil limit removes it: the account
	// is not restricted, and is served with no lease.
	SetTimeLimit(ctx context.Context, userID, libraryID string, lim *TimeLimit) error
	// TimeServed is when streaming last served media of that stream session (or, for
	// direct play, of that video) to the account. known is false when streaming
	// holds no lease for that session, so it cannot tell.
	TimeServed(ctx context.Context, userID, libraryID, sessionID, videoID string) (last time.Time, known bool, err error)
}

// sessionMark is one open stream session of a restricted account.
type sessionMark struct {
	userID, libraryID, videoID string
	at                         time.Time     // counted up to here
	seen                       time.Time     // the last call for the session
	carry                      time.Duration // the part of a second not yet counted
	debt                       int           // seconds a failed write still owes
	lapsed                     bool          // found quiet for longer than HeartbeatCap
}

// TimeKeeper answers and counts for the API. The zero value is not usable: use
// NewTimeKeeper.
type TimeKeeper struct {
	cache *Cache
	store *Store
	// Push is the streaming service (nil: none, so nothing but the count is kept).
	Push TimePusher
	// MarksKept bounds the session marks (0: marksKept). When it is reached the
	// mark heard from longest ago is counted, its lease is ended, and it is dropped:
	// a session is never served without a count.
	MarksKept int

	mu    sync.Mutex
	marks map[string]*sessionMark // by stream session id
}

func NewTimeKeeper(cache *Cache, push TimePusher) *TimeKeeper {
	return &TimeKeeper{cache: cache, store: cache.Store(), Push: push, marks: map[string]*sessionMark{}}
}

// marksKept bounds the session marks; a mark not heard from for markIdle, which
// streaming says it serves nothing, is dropped once its lease is ended.
const (
	marksKept = 4096
	markIdle  = 6 * time.Hour
)

// Time answers whether p may play in libraryID now ("": the account alone), for
// how long, and why not. An admin or an unrestricted account is allowed at once,
// with no time query. An error means the answer is not known: the caller refuses.
func (k *TimeKeeper) Time(ctx context.Context, p *principal.Principal, libraryID string) (TimeState, error) {
	if on, err := k.counted(ctx, p); err != nil || !on {
		return TimeState{Allowed: err == nil}, err
	}
	return k.store.TimeOf(ctx, p.UserID, libraryID)
}

// counted reports whether p is a restricted account, whose time is kept.
func (k *TimeKeeper) counted(ctx context.Context, p *principal.Principal) (bool, error) {
	if p == nil {
		return false, errors.New("parental time: no principal")
	}
	if p.IsAdmin {
		return false, nil
	}
	eff, err := k.cache.For(ctx, p)
	if err != nil {
		return false, err
	}
	return eff != nil && eff.Restricted, nil
}

// Admit is Time for a stream about to start or renew. What the account's other
// sessions still owe is counted first. When it is allowed, streaming is told when
// the account's playback must end before Admit returns; if streaming cannot be
// told, or the count cannot be written, Admit returns the error and the stream is
// refused. Admit gives no lease: Opened does, for the session that was opened.
func (k *TimeKeeper) Admit(ctx context.Context, p *principal.Principal, libraryID string) (TimeState, error) {
	if on, err := k.counted(ctx, p); err != nil || !on {
		return TimeState{Allowed: err == nil}, err
	}
	if err := k.settle(ctx, p.UserID, ""); err != nil {
		return TimeState{}, err
	}
	st, err := k.store.TimeOf(ctx, p.UserID, libraryID)
	if err != nil {
		return TimeState{}, err
	}
	if !st.Allowed {
		_ = k.push(ctx, p.UserID, libraryID, st, leaseKeep, "", "") // a stream still running ends now
		return st, nil
	}
	if err := k.push(ctx, p.UserID, libraryID, st, leaseKeep, "", ""); err != nil {
		return TimeState{}, err
	}
	return st, nil
}

type leaseMode int

const (
	leaseKeep  leaseMode = iota // leave every lease as it is
	leaseRenew                  // a counted call came for the session: HeartbeatCap from now
	leaseEnd                    // the session closed, or its mark is let go: ends now
)

func (k *TimeKeeper) push(ctx context.Context, userID, libraryID string, st TimeState, lease leaseMode, sessionID, videoID string) error {
	if k.Push == nil {
		return nil
	}
	lim := &TimeLimit{}
	switch {
	case !st.Allowed:
		now := st.Now
		lim.EndAt, lim.Reason, lim.NextAllowedAt = &now, st.Reason, st.NextAllowedAt
	case st.EndsAt != nil:
		lim.EndAt, lim.Reason = st.EndsAt, ReasonLimitReached
		if st.SlotEndsAt != nil && !st.SlotEndsAt.After(*st.EndsAt) {
			lim.Reason = ReasonOutsideSlot
		}
	}
	if lease != leaseKeep && sessionID != "" {
		until := st.Now
		if lease == leaseRenew {
			until = until.Add(HeartbeatCap)
		}
		lim.SessionID, lim.VideoID, lim.LeaseUntil = sessionID, videoID, &until
	}
	return k.Push.SetTimeLimit(ctx, userID, libraryID, lim)
}

// lease renews or ends one session's lease, with the account's end as it is now,
// which it returns.
func (k *TimeKeeper) lease(ctx context.Context, m sessionMark, sessionID string, mode leaseMode) (TimeState, error) {
	st, err := k.store.TimeOf(ctx, m.userID, m.libraryID)
	if err != nil {
		return TimeState{}, err
	}
	return st, k.push(ctx, m.userID, m.libraryID, st, mode, sessionID, m.videoID)
}

// restricted reports whether userID's playback is counted.
func (k *TimeKeeper) restricted(ctx context.Context, userID string) (bool, error) {
	eff, err := k.cache.Effective(ctx, userID)
	if err != nil {
		return false, err
	}
	return eff != nil && eff.Restricted, nil
}

// owed is the time to count for a mark counted up to m.at, and whether that is
// known. Within HeartbeatCap of a call for the session it is the whole gap. Past it
// the session's lease ran out at m.seen+HeartbeatCap, so what streaming says it
// served the session is counted. When streaming cannot say, the rest of the lease
// is counted whole, and sure is false unless streaming holds no lease for the
// session at all (it serves it nothing).
func (k *TimeKeeper) owed(ctx context.Context, m sessionMark, sessionID string, now time.Time) (d time.Duration, sure bool) {
	gap := now.Sub(m.at)
	if gap <= 0 {
		return 0, true
	}
	if !m.lapsed && gap <= HeartbeatCap {
		return gap, true
	}
	// what the lease still allowed after m.at
	left := max(0, min(gap, m.seen.Add(HeartbeatCap).Sub(m.at)))
	if k.Push == nil {
		return left, false
	}
	served, known, err := k.Push.TimeServed(ctx, m.userID, m.libraryID, sessionID, m.videoID)
	if err != nil {
		return left, false
	}
	if !known {
		return left, true
	}
	return max(0, min(served.Sub(m.at), gap)), true
}

// count adds what the mark owes up to now. heard: this is a call for the session
// itself, not the keeper finding it quiet. On a failed write the seconds stay owed
// on the mark, and the error is returned: the caller does not renew the lease.
func (k *TimeKeeper) count(ctx context.Context, sessionID string, m *sessionMark, now time.Time, heard bool) (seconds int, sure bool, err error) {
	k.mu.Lock()
	snap := *m
	if now.After(m.at) {
		m.at = now
	}
	if heard {
		m.seen = now
	}
	m.lapsed = !heard
	m.carry, m.debt = 0, 0
	k.mu.Unlock()

	d, sure := k.owed(ctx, snap, sessionID, now)
	d += snap.carry
	seconds = int(d/time.Second) + snap.debt
	if seconds > 0 {
		if err := k.store.AddUsage(ctx, snap.userID, snap.libraryID, seconds); err != nil {
			k.mu.Lock()
			m.debt += seconds
			k.mu.Unlock()
			return 0, sure, fmt.Errorf("parental time: count: %w", err)
		}
	}
	k.mu.Lock()
	m.carry += d % time.Second
	k.mu.Unlock()
	return seconds, sure, nil
}

// letGo ends the session's lease in streaming and then drops its mark: from here
// on streaming serves the session nothing until the API counts it again. If
// streaming cannot be told, the mark stays.
func (k *TimeKeeper) letGo(ctx context.Context, sessionID string, m *sessionMark) error {
	k.mu.Lock()
	snap := *m
	k.mu.Unlock()
	if _, err := k.lease(ctx, snap, sessionID, leaseEnd); err != nil {
		return err
	}
	k.mu.Lock()
	if k.marks[sessionID] == m {
		delete(k.marks, sessionID)
	}
	k.mu.Unlock()
	return nil
}

// settle counts what userID's other sessions owe once they have gone quiet for
// longer than HeartbeatCap (a session that stopped calling, or never called). A
// mark is let go only when it was not heard from for markIdle and streaming says it
// served the session nothing since. except is the session being counted by the
// caller.
func (k *TimeKeeper) settle(ctx context.Context, userID, except string) error {
	now := k.store.now()
	type due struct {
		id string
		m  *sessionMark
	}
	var list []due
	k.mu.Lock()
	for id, m := range k.marks {
		if m.userID == userID && id != except && (now.Sub(m.at) > HeartbeatCap || m.debt > 0) {
			list = append(list, due{id, m})
		}
	}
	k.mu.Unlock()
	var first error
	for _, d := range list {
		seconds, sure, err := k.count(ctx, d.id, d.m, now, false)
		if err == nil && sure && seconds == 0 && now.Sub(d.m.seen) > markIdle {
			err = k.letGo(ctx, d.id, d.m)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// mark returns the session's mark, making it when there is none. made reports a new
// one. When the marks are full, the one heard from longest ago is counted from what
// streaming served it and let go first; if that cannot be done the new session gets
// no mark and the error is returned, so it is refused.
func (k *TimeKeeper) mark(ctx context.Context, sessionID string, fresh sessionMark) (m *sessionMark, made bool, err error) {
	limit := k.MarksKept
	if limit <= 0 {
		limit = marksKept
	}
	for {
		k.mu.Lock()
		if m = k.marks[sessionID]; m != nil && m.userID == fresh.userID {
			k.mu.Unlock()
			return m, false, nil
		}
		if len(k.marks) < limit {
			m = &fresh
			k.marks[sessionID] = m
			k.mu.Unlock()
			return m, true, nil
		}
		oldest := ""
		for id, o := range k.marks {
			if oldest == "" || o.seen.Before(k.marks[oldest].seen) {
				oldest = id
			}
		}
		old := k.marks[oldest]
		k.mu.Unlock()
		if _, _, err := k.count(ctx, oldest, old, k.store.now(), false); err != nil {
			return nil, false, err
		}
		if err := k.letGo(ctx, oldest, old); err != nil {
			return nil, false, err
		}
	}
}

// Opened marks a stream session the API just opened or renewed for userID, after
// Admit allowed it, and gives it its lease. A renewed session is counted like any
// other call for it. On an error the session has no lease: the caller refuses it.
func (k *TimeKeeper) Opened(ctx context.Context, userID, libraryID, videoID, sessionID string) error {
	on, err := k.restricted(ctx, userID)
	if err != nil {
		return err
	}
	if !on {
		k.forget(sessionID)
		return nil
	}
	now := k.store.now()
	m, made, err := k.mark(ctx, sessionID, sessionMark{userID: userID, libraryID: libraryID, videoID: videoID, at: now, seen: now})
	if err != nil {
		return err
	}
	if !made {
		if _, _, err := k.count(ctx, sessionID, m, now, true); err != nil {
			return err
		}
	}
	_, err = k.lease(ctx, *m, sessionID, leaseRenew)
	return err
}

// Kept reports whether p's playback time is kept: p is a restricted account. Every
// stream URL of such an account names its session, so streaming serves it only
// under that session's lease.
func (k *TimeKeeper) Kept(ctx context.Context, p *principal.Principal) (bool, error) {
	return k.counted(ctx, p)
}

// Progress is Beat for a caller that does not need the state.
func (k *TimeKeeper) Progress(ctx context.Context, userID, sessionID string) error {
	_, err := k.Beat(ctx, userID, sessionID)
	return err
}

// Beat counts one progress post of a stream session: the stream's heartbeat. It
// adds the time since the last call for the session, never more than HeartbeatCap,
// whatever position the client reports, and renews streaming's lease. A session the
// home did not open for userID is ErrUnknownSession: it is not counted as another's
// and renews nothing. On an error nothing is renewed, so the stream ends when its
// lease does. It returns the account's time as it is after the count: not Allowed
// means the stream is over. An unrestricted account costs no query.
func (k *TimeKeeper) Beat(ctx context.Context, userID, sessionID string) (TimeState, error) {
	on, err := k.restricted(ctx, userID)
	if err != nil {
		return TimeState{}, err
	}
	if !on {
		k.forget(sessionID)
		return TimeState{Allowed: true}, nil
	}
	now := k.store.now()
	k.mu.Lock()
	m := k.marks[sessionID]
	k.mu.Unlock()
	if m == nil || m.userID != userID {
		// No mark (the API restarted, or the mark was dropped): the session's own row
		// says whose it is and since when it has run.
		var owner, video, lib string
		var opened time.Time
		err := k.store.DB.QueryRowContext(ctx, `
			SELECT s.user_id::text, s.video_id::text, COALESCE(v.library_id::text, ''), s.opened_at
			  FROM streaming_sessions s LEFT JOIN videos v ON v.id = s.video_id
			 WHERE s.id::text = $1`, sessionID).Scan(&owner, &video, &lib, &opened)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != userID) {
			return TimeState{}, ErrUnknownSession
		}
		if err != nil {
			return TimeState{}, err
		}
		if opened.After(now) {
			opened = now
		}
		if m, _, err = k.mark(ctx, sessionID, sessionMark{userID: userID, libraryID: lib, videoID: video, at: opened, seen: now}); err != nil {
			return TimeState{}, err
		}
	}
	if _, _, err := k.count(ctx, sessionID, m, now, true); err != nil {
		return TimeState{}, err
	}
	if err := k.settle(ctx, userID, sessionID); err != nil {
		return TimeState{}, err
	}
	return k.lease(ctx, *m, sessionID, leaseRenew)
}

// Closed counts a stream session up to its close, ends its lease and drops its
// mark. On an error the mark stays: what it owes is counted with the next call.
func (k *TimeKeeper) Closed(ctx context.Context, sessionID string) error {
	k.mu.Lock()
	m := k.marks[sessionID]
	k.mu.Unlock()
	if m == nil {
		return nil
	}
	if _, _, err := k.count(ctx, sessionID, m, k.store.now(), true); err != nil {
		return err
	}
	return k.letGo(ctx, sessionID, m)
}

func (k *TimeKeeper) forget(sessionID string) {
	k.mu.Lock()
	delete(k.marks, sessionID)
	k.mu.Unlock()
}

// Refresh tells streaming again when userID's playback must end, after the rules or
// a grant changed: for the libraries named, and for every library a session of that
// account is known to be playing in. It renews no lease.
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
	on, err := k.restricted(ctx, userID)
	if err != nil {
		return err
	}
	var first error
	for lib := range libs {
		var err error
		if !on {
			err = k.Push.SetTimeLimit(ctx, userID, lib, nil)
		} else {
			var st TimeState
			if st, err = k.store.TimeOf(ctx, userID, lib); err == nil {
				err = k.push(ctx, userID, lib, st, leaseKeep, "", "")
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
