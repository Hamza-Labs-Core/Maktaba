package streaming

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ledgerKeep is how long a node remembers a session it opened: a day outlasts any
// playback (sessions idle out after minutes) without growing for ever.
const ledgerKeep = 24 * time.Hour

// ledger is a full node's own record of the sessions it opened (the owner and the
// video), so it can close one and say whose it is while the home's database, which
// holds the streaming_sessions rows, does not answer. A session whose row could not be
// written then, or whose closing could not, is written once the database answers again
// (Handler.Reconcile): the home's history has it, and the progress the player reports
// on it (the home's route) finds it. It is kept in the node's state directory (path),
// so a restart of this API writes them back too.
type ledger struct {
	mu   sync.Mutex
	byID map[string]ledgerEntry
	// path is where it is kept ("": in memory only); loaded: read from it already
	path   string
	loaded bool
}

type ledgerEntry struct {
	Owner  string    `json:"owner"`
	Video  string    `json:"video"`
	Mode   string    `json:"mode"`
	Opened time.Time `json:"opened"`
	// Row is what the streaming_sessions row is written with
	Row OpenSessionRequest `json:"row"`
	// Written: the row is in the database; Closed: when it was closed (zero: open);
	// CloseWritten: its closing is in the database too
	Written      bool      `json:"written"`
	Closed       time.Time `json:"closed"`
	CloseWritten bool      `json:"close_written"`
}

// load reads the ledger once (held lock).
func (l *ledger) load() {
	if l.byID == nil {
		l.byID = map[string]ledgerEntry{}
	}
	if l.loaded || l.path == "" {
		return
	}
	l.loaded = true
	if b, err := os.ReadFile(l.path); err == nil {
		_ = json.Unmarshal(b, &l.byID)
	}
}

// save keeps the ledger (held lock), on disk before it replaces the last one (a power
// cut leaves one or the other whole); best-effort: a failure leaves it in memory.
func (l *ledger) save() {
	if l.path == "" {
		return
	}
	b, err := json.Marshal(l.byID)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return
	}
	tmp := l.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	serr := f.Sync()
	if cerr := f.Close(); werr != nil || serr != nil || cerr != nil {
		_ = os.Remove(tmp)
		return
	}
	if os.Rename(tmp, l.path) != nil {
		return
	}
	if d, err := os.Open(filepath.Dir(l.path)); err == nil {
		_ = d.Sync() // the rename itself (not on every system)
		_ = d.Close()
	}
}

// open records a session; false when the id is another user's session.
func (l *ledger) open(id, owner string, row OpenSessionRequest, mode string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	if e, ok := l.byID[id]; ok && e.Owner != owner {
		return false
	}
	for k, e := range l.byID {
		if now.Sub(e.Opened) > ledgerKeep {
			delete(l.byID, k)
		}
	}
	l.byID[id] = ledgerEntry{Owner: owner, Video: row.VideoID, Mode: mode, Opened: now, Row: row}
	l.save()
	return true
}

func (l *ledger) get(id string) (ledgerEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	e, ok := l.byID[id]
	return e, ok
}

// wrote records that the session's row is in the database (with its closing, when it
// was written closed).
func (l *ledger) wrote(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	if e, ok := l.byID[id]; ok {
		if !e.Closed.IsZero() {
			delete(l.byID, id)
		} else {
			e.Written = true
			l.byID[id] = e
		}
		l.save()
	}
}

// close records that a session was closed: kept until its closing is in the database
// (closeWritten), or its row is written closed.
func (l *ledger) close(id string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	if e, ok := l.byID[id]; ok && e.Closed.IsZero() {
		e.Closed = now
		l.byID[id] = e
		l.save()
	}
}

// closeWritten records that the session's closing is in the database: it is forgotten
// (one whose row is not written yet is kept: it is written closed).
func (l *ledger) closeWritten(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	if e, ok := l.byID[id]; ok && e.Written {
		delete(l.byID, id)
		l.save()
	}
}

// drop forgets a session (its id was another user's).
func (l *ledger) drop(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	delete(l.byID, id)
	l.save()
}

// pending are the sessions the database lacks: their rows, or their closing.
func (l *ledger) pending() map[string]ledgerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	out := map[string]ledgerEntry{}
	for k, e := range l.byID {
		if !e.Written || (!e.Closed.IsZero() && !e.CloseWritten) {
			out[k] = e
		}
	}
	return out
}
