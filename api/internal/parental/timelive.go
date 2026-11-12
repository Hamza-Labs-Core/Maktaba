package parental

// Live TV under the time rules (story 57.14 §2 and §3): a channel counts like any
// playback, and is refused and cut by the same rules.
//
// A channel's stream has no stream session, so nothing here keeps a mark or a
// lease for it. The count comes from the stream itself: the streaming service's
// live TV guard measures how long it served a viewer a channel and says so each
// time it asks the verdict (LiveServed). A viewer away from home watches from the
// home's Cloudflare account, which the home cannot see, so a remote viewing
// session is counted from its heartbeats instead (RemoteBeat): the session ends
// without them, so they are the stream's own sign of life, not a position the
// client reports.
//
// What stays uncounted: a channel played through the tuner (Plex, a TV on the
// allowed list) or by the admin token names no account, so there is no one to
// count it for.

import (
	"context"
	"time"
)

const (
	// MaxLiveServed bounds what one report of served live TV adds: the guard
	// reports every few seconds, so more than this is not a report.
	MaxLiveServed = 10 * time.Minute
	// RemoteBeatCap bounds what one heartbeat of a remote viewing session adds: a
	// session with no heartbeat for 150 s is ended (tvremote.SessionTimeout), so a
	// longer gap is not viewing.
	RemoteBeatCap = 5 * time.Minute

	remoteMarksKept = 4096
)

// TimeOfUser is Time for an account named by id (the streaming service asks by
// id). An account that is not restricted is allowed at once.
func (k *TimeKeeper) TimeOfUser(ctx context.Context, userID, libraryID string) (TimeState, error) {
	on, err := k.restricted(ctx, userID)
	if err != nil || !on {
		return TimeState{Allowed: err == nil}, err
	}
	return k.store.TimeOf(ctx, userID, libraryID)
}

// LiveServed counts served of live TV the streaming service served userID from a
// channel of libraryID ("": a channel with no library), and answers the account's
// time after it. On an error nothing is known: the caller refuses the stream, and
// the streaming service reports the same time again.
func (k *TimeKeeper) LiveServed(ctx context.Context, userID, libraryID string, served time.Duration) (TimeState, error) {
	on, err := k.restricted(ctx, userID)
	if err != nil || !on {
		return TimeState{Allowed: err == nil}, err
	}
	return k.liveCount(ctx, userID, libraryID, min(served, MaxLiveServed))
}

func (k *TimeKeeper) liveCount(ctx context.Context, userID, libraryID string, d time.Duration) (TimeState, error) {
	seconds := int(d / time.Second)
	if seconds > 0 {
		if err := k.store.AddUsage(ctx, userID, libraryID, seconds); err != nil {
			return TimeState{}, err
		}
	}
	st, err := k.store.TimeOf(ctx, userID, libraryID)
	if err != nil {
		return TimeState{}, err
	}
	if !st.Allowed && seconds > 0 {
		_ = k.store.RecordTimeUp(ctx, userID, st.Reason) // the activity note; the stream ends either way
	}
	return st, nil
}

// RemoteBeat counts one call for a remote viewing session of userID (a tune that
// finds it open, or its heartbeat): the time since the keeper last counted the
// session or, when it holds no mark for it (the API restarted), since lastBeat, the
// session's own last heartbeat. Never more than RemoteBeatCap. It answers the
// account's time after the count.
func (k *TimeKeeper) RemoteBeat(ctx context.Context, userID, libraryID, sessionID string, lastBeat time.Time) (TimeState, error) {
	on, err := k.restricted(ctx, userID)
	if err != nil || !on {
		return TimeState{Allowed: err == nil}, err
	}
	now := k.store.now()
	k.mu.Lock()
	from, marked := k.remote[sessionID]
	if !marked {
		from = lastBeat
	}
	if k.remote == nil {
		k.remote = map[string]time.Time{}
	}
	if len(k.remote) >= remoteMarksKept {
		// a mark that is gone is read from the session's own last heartbeat again
		for id, at := range k.remote {
			if now.Sub(at) > RemoteBeatCap {
				delete(k.remote, id)
			}
		}
		if len(k.remote) >= remoteMarksKept {
			k.remote = map[string]time.Time{}
		}
	}
	k.remote[sessionID] = now
	k.mu.Unlock()
	st, err := k.liveCount(ctx, userID, libraryID, max(0, min(now.Sub(from), RemoteBeatCap)))
	if err != nil {
		// not counted: the next call counts from where this one started
		k.mu.Lock()
		k.remote[sessionID] = from
		k.mu.Unlock()
	}
	return st, err
}
