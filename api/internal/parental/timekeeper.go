package parental

// The time keeper (57.14): the one place the API asks whether a restricted account
// may play now, counts what it watched, and tells streaming when a running stream
// must end.
//
// The count is the home's own. A stream session is marked when the API opens it,
// and every call the API gets for it (open, renew, progress post, close) adds the
// time since the one before, never more than HeartbeatCap. What the client says its
// position is plays no part. Streaming serves a time-limited account only for
// HeartbeatCap after the last counted call (the lease), so a client that stops
// calling is stopped, and what it was served before that is counted from what
// streaming says it served.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// ReasonNoHeartbeat is the reason streaming gives when it stopped serving a
// time-limited account because no counted call came within HeartbeatCap. A progress
// post or a renewed session opens it again.
const ReasonNoHeartbeat = "no_heartbeat"

// ErrUnknownSession: the stream session is not one the home opened for that account.
var ErrUnknownSession = errors.New("parental time: not a stream session of that account")

// TimeLimit is what streaming holds for one account in one library.
type TimeLimit struct {
	// EndAt: no media is served from here on (nil: no end).
	EndAt *time.Time
	// LeaseUntil: no media is served from here on until a newer lease comes. Nil
	// keeps the lease streaming has (none: nothing is served until one comes).
	LeaseUntil *time.Time
	// Reason and NextAllowedAt are what streaming answers once EndAt has passed.
	Reason        string
	NextAllowedAt *time.Time
}

// TimePusher is the streaming service, as the time keeper needs it.
type TimePusher interface {
	// SetTimeLimit sets what streaming holds for the account in the library
	// (libraryID "": playback with no library). A nil limit removes it: the account
	// has no time limit there.
	SetTimeLimit(ctx context.Context, userID, libraryID string, lim *TimeLimit) error
	// TimeServed is when streaming last served media of that stream session (or, for
	// direct play, of that video) to the account. known is false when streaming
	// holds no limit for the account in the library, so it cannot tell.
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
	// mark heard from longest ago is counted and dropped: a session is never left
	// without a count.
	MarksKept int

	mu    sync.Mutex
	marks map[string]*sessionMark // by stream session id
}

func NewTimeKeeper(cache *Cache, push TimePusher) *TimeKeeper {
	return &TimeKeeper{cache: cache, store: cache.Store(), Push: push, marks: map[string]*sessionMark{}}
}

// marksKept bounds the session marks; a mark not heard from for markIdle is dropped
// once what it owes is counted.
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
// sessions still owe is counted first. When it is allowed for a time-limited
// account, streaming is told when it must end before Admit returns; if streaming
// cannot be told, or the count cannot be written, Admit returns the error and the
// stream is refused.
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
		_ = k.push(ctx, p.UserID, libraryID, st, leaseKeep) // a stream still running ends now
		return st, nil
	}
	if err := k.push(ctx, p.UserID, libraryID, st, leaseRenew); err != nil {
		return TimeState{}, err
	}
	return st, nil
}

type leaseMode int

const (
	leaseKeep  leaseMode = iota // leave the lease streaming has
	leaseRenew                  // a counted call came: HeartbeatCap from now
	leaseEnd                    // the last session there closed: ends now
)

func (k *TimeKeeper) push(ctx context.Context, userID, libraryID string, st TimeState, lease leaseMode) error {
	if k.Push == nil {
		return nil
	}
	if !st.Allowed {
		now := st.Now
		return k.Push.SetTimeLimit(ctx, userID, libraryID, &TimeLimit{EndAt: &now, Reason: st.Reason, NextAllowedAt: st.NextAllowedAt})
	}
	if st.EndsAt == nil {
		return k.Push.SetTimeLimit(ctx, userID, libraryID, nil) // no rule covers it: no limit
	}
	lim := &TimeLimit{EndAt: st.EndsAt, Reason: ReasonLimitReached}
	if st.SlotEndsAt != nil && !st.SlotEndsAt.After(*st.EndsAt) {
		lim.Reason = ReasonOutsideSlot
	}
	switch lease {
	case leaseRenew:
		until := st.Now.Add(HeartbeatCap)
		lim.LeaseUntil = &until
	case leaseEnd:
		until := st.Now
		lim.LeaseUntil = &until
	}
	return k.Push.SetTimeLimit(ctx, userID, libraryID, lim)
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
// known. Within HeartbeatCap of a call it is the whole gap. Past it the lease ran
// out in between, so streaming served the session nothing after m.at+HeartbeatCap:
// what streaming says it served up to then is counted. A mark already found quiet
// (lapsed) has no lease of its own, so whatever streaming served it since is
// counted. When streaming cannot say, the whole of HeartbeatCap is, and sure is
// false.
func (k *TimeKeeper) owed(ctx context.Context, m sessionMark, sessionID string, now time.Time) (d time.Duration, sure bool) {
	gap := now.Sub(m.at)
	if gap <= 0 {
		return 0, true
	}
	if !m.lapsed && gap <= HeartbeatCap {
		return gap, true
	}
	if k.Push == nil {
		return min(gap, HeartbeatCap), false
	}
	served, known, err := k.Push.TimeServed(ctx, m.userID, m.libraryID, sessionID, m.videoID)
	if err != nil || !known {
		return min(gap, HeartbeatCap), false
	}
	limit := HeartbeatCap
	if m.lapsed {
		limit = gap
	}
	return max(0, min(served.Sub(m.at), limit)), true
}

// count adds what the mark owes up to now. heard: this is a call for the session
// itself, not the keeper finding it quiet. On a failed write the seconds stay owed
// on the mark, and the error is returned: the caller does not renew the lease.
func (k *TimeKeeper) count(ctx context.Context, sessionID string, m *sessionMark, now time.Time, heard bool) (sure bool, err error) {
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
	seconds := int(d/time.Second) + snap.debt
	if seconds > 0 {
		if err := k.store.AddUsage(ctx, snap.userID, snap.libraryID, seconds); err != nil {
			k.mu.Lock()
			m.debt += seconds
			k.mu.Unlock()
			return sure, fmt.Errorf("parental time: count: %w", err)
		}
	}
	k.mu.Lock()
	m.carry += d % time.Second
	k.mu.Unlock()
	return sure, nil
}

// settle counts what userID's other sessions owe once they have gone quiet for
// longer than HeartbeatCap (a session that stopped calling, or never called). It
// drops the ones not heard from for markIdle, and the ones streaming could not
// answer for, which were counted a whole interval. except is the session being
// counted by the caller.
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
		sure, err := k.count(ctx, d.id, d.m, now, false)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		k.mu.Lock()
		if (!sure || now.Sub(d.m.seen) > markIdle) && k.marks[d.id] == d.m {
			delete(k.marks, d.id)
		}
		k.mu.Unlock()
	}
	return first
}

// mark returns the session's mark, making it when there is none. made reports a new
// one. When the marks are full the one heard from longest ago is counted (the whole
// of HeartbeatCap at most) and dropped first.
func (k *TimeKeeper) mark(ctx context.Context, sessionID string, fresh sessionMark) (m *sessionMark, made bool, err error) {
	k.mu.Lock()
	if m = k.marks[sessionID]; m != nil && m.userID == fresh.userID {
		k.mu.Unlock()
		return m, false, nil
	}
	limit := k.MarksKept
	if limit <= 0 {
		limit = marksKept
	}
	var evicted []sessionMark
	for len(k.marks) >= limit {
		oldest := ""
		for id, o := range k.marks {
			if oldest == "" || o.seen.Before(k.marks[oldest].seen) {
				oldest = id
			}
		}
		evicted = append(evicted, *k.marks[oldest])
		delete(k.marks, oldest)
	}
	m = &fresh
	k.marks[sessionID] = m
	k.mu.Unlock()

	now := k.store.now()
	for _, o := range evicted {
		d := min(max(now.Sub(o.at), 0), HeartbeatCap) + o.carry
		if seconds := int(d/time.Second) + o.debt; seconds > 0 {
			if aerr := k.store.AddUsage(ctx, o.userID, o.libraryID, seconds); aerr != nil && err == nil {
				err = fmt.Errorf("parental time: count: %w", aerr)
			}
		}
	}
	return m, true, err
}

// Opened marks a stream session the API just opened or renewed for userID, after
// Admit allowed it. A renewed session is counted like any other call for it.
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
	if err != nil || made {
		return err
	}
	_, err = k.count(ctx, sessionID, m, now, true)
	return err
}

// Progress counts one progress post of a stream session: the stream's heartbeat. It
// adds the time since the last call for the session, never more than HeartbeatCap,
// whatever position the client reports, and renews streaming's lease. A session the
// home did not open for userID is ErrUnknownSession: it is not counted as another's
// and renews nothing. On an error nothing is renewed, so the stream ends when its
// lease does. An unrestricted account costs no query.
func (k *TimeKeeper) Progress(ctx context.Context, userID, sessionID string) error {
	on, err := k.restricted(ctx, userID)
	if err != nil {
		return err
	}
	if !on {
		k.forget(sessionID)
		return nil
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
			return ErrUnknownSession
		}
		if err != nil {
			return err
		}
		if opened.After(now) {
			opened = now
		}
		if m, _, err = k.mark(ctx, sessionID, sessionMark{userID: userID, libraryID: lib, videoID: video, at: opened, seen: now}); err != nil {
			return err
		}
	}
	if _, err := k.count(ctx, sessionID, m, now, true); err != nil {
		return err
	}
	if err := k.settle(ctx, userID, sessionID); err != nil {
		return err
	}
	st, err := k.store.TimeOf(ctx, userID, m.libraryID)
	if err != nil {
		return err
	}
	return k.push(ctx, userID, m.libraryID, st, leaseRenew)
}

// Closed counts a stream session up to its close and drops its mark. When it was
// the account's last session in that library, streaming's lease there ends now.
func (k *TimeKeeper) Closed(ctx context.Context, sessionID string) error {
	k.mu.Lock()
	m := k.marks[sessionID]
	k.mu.Unlock()
	if m == nil {
		return nil
	}
	now := k.store.now()
	if _, err := k.count(ctx, sessionID, m, now, true); err != nil {
		return err // the mark stays: what it owes is counted with the next call
	}
	k.mu.Lock()
	delete(k.marks, sessionID)
	others := false
	for _, o := range k.marks {
		if o.userID == m.userID && o.libraryID == m.libraryID && now.Sub(o.seen) <= HeartbeatCap {
			others = true
		}
	}
	k.mu.Unlock()
	if others {
		return nil
	}
	st, err := k.store.TimeOf(ctx, m.userID, m.libraryID)
	if err != nil {
		return err
	}
	return k.push(ctx, m.userID, m.libraryID, st, leaseEnd)
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
				err = k.push(ctx, userID, lib, st, leaseKeep)
			}
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
