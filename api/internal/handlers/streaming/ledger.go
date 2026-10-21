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
// holds the streaming_sessions rows, does not answer. In memory: a session does not
// outlive a restart of the streaming role either.
type ledger struct {
	mu   sync.Mutex
	byID map[string]ledgerEntry
}

type ledgerEntry struct {
	owner, video, mode string
	opened             time.Time
}

// open records a session; false when the id is another user's session.
func (l *ledger) open(id, owner, video, mode string, now time.Time) bool {
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
	l.byID[id] = ledgerEntry{owner: owner, video: video, mode: mode, opened: now}
	return true
}

func (l *ledger) get(id string) (ledgerEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.byID[id]
	return e, ok
}

func (l *ledger) close(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byID, id)
}
