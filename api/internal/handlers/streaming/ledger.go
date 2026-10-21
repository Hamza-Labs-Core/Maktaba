package streaming

import (
	"sync"
	"time"
)

// ledgerKeep is how long a node remembers a session it opened: a day outlasts any
// playback (sessions idle out after minutes) without growing for ever.
const ledgerKeep = 24 * time.Hour

// ledger is a full node's own record of the sessions it opened (the owner and the
// video), so it can close one and say whose it is while the home's database, which
// holds the streaming_sessions rows, does not answer. A session whose row could not be
// written then is written once the database answers again (Handler.Reconcile): the
// home's history has it, and the progress the player reports on it (the home's route)
// finds it. In memory: a session does not outlive a restart of the streaming role
// either.
type ledger struct {
	mu   sync.Mutex
	byID map[string]ledgerEntry
}

type ledgerEntry struct {
	owner, video, mode string
	opened             time.Time
	// row is what the streaming_sessions row is written with
	row OpenSessionRequest
	// written: the row is in the database; closed: when it was closed (zero: open)
	written bool
	closed  time.Time
}

// open records a session; false when the id is another user's session.
func (l *ledger) open(id, owner string, row OpenSessionRequest, mode string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byID == nil {
		l.byID = map[string]ledgerEntry{}
	}
	if e, ok := l.byID[id]; ok && e.owner != owner {
		return false
	}
	for k, e := range l.byID {
		if now.Sub(e.opened) > ledgerKeep {
			delete(l.byID, k)
		}
	}
	l.byID[id] = ledgerEntry{owner: owner, video: row.VideoID, mode: mode, opened: now, row: row}
	return true
}

func (l *ledger) get(id string) (ledgerEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.byID[id]
	return e, ok
}

// wrote records that the session's row is in the database.
func (l *ledger) wrote(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.byID[id]; ok {
		if !e.closed.IsZero() {
			delete(l.byID, id)
			return
		}
		e.written = true
		l.byID[id] = e
	}
}

// close forgets a closed session, or keeps one whose row is not written yet (it is
// written closed).
func (l *ledger) close(id string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.byID[id]
	switch {
	case !ok:
	case e.written:
		delete(l.byID, id)
	case e.closed.IsZero():
		e.closed = now
		l.byID[id] = e
	}
}

// drop forgets a session (its id was another user's).
func (l *ledger) drop(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byID, id)
}

// unwritten are the sessions whose rows are not in the database yet.
func (l *ledger) unwritten() map[string]ledgerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]ledgerEntry{}
	for k, e := range l.byID {
		if !e.written {
			out[k] = e
		}
	}
	return out
}
