package browse

// The per-library rows (a pinned library's row on the home, a hub's recently
// added) from memory: one library's newest visible videos, kept per process.
//
// What a caller may see in a library's row is the same for everyone who may
// see the library: the row's predicate (visible) has no per-user part, and its
// cards carry no per-user field (playback is on other rows). Access is by
// library, checked from the caller's scope before the cache is read. So an
// entry is keyed by library (and the row's length) alone.
//
// A request answers from the entry at once. An entry older than the freshness
// window is read again in the background (one read per entry at a time), the
// request still answering from it; an entry not there is read before
// answering (callers wanting it at once share one read).
//
// Entries are dropped when their library's videos change, on every API
// process: slot 0166's trigger notifies browse.library_changed with the
// library id for every insert, delete and change of a card's column in
// videos, whoever writes it (the pipeline on any machine, the API, an admin's
// SQL), and Listen hears it. While the listener is not connected nothing is
// kept (the rows are read from the database), and everything is dropped when
// it connects again: a change it missed is never served.

import (
	"container/list"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// LibraryChangedChannel is the NOTIFY channel slot 0166's trigger fires on;
// its payload is the library id, or "*" for every library.
const LibraryChangedChannel = "browse.library_changed"

const (
	// rowFresh is how long an entry is served without a background read.
	rowFresh = 30 * time.Second
	// rowEntries caps the entries (a library's row and its hub scan are two).
	rowEntries = 1024
	// rowReadTimeout bounds a background read.
	rowReadTimeout = 30 * time.Second
)

type rowKey struct {
	lib string
	n   int
}

type rowEntry struct {
	key   rowKey
	cards []Card // read-only: shared by every caller
	at    time.Time
	has   bool
	read  *rowRead // a read in flight
	dead  bool     // dropped: a read in flight keeps its answer to itself
	elem  *list.Element
}

type rowRead struct {
	done chan struct{}
	got  map[string][]Card
	err  error
}

// rowLoader reads up to n of the newest visible videos of each library.
type rowLoader func(ctx context.Context, libs []string, n int) (map[string][]Card, error)

// RowCache keeps libraries' rows. Its zero value is not usable: NewRowCache.
// It keeps nothing until Listen has connected (Resume).
type RowCache struct {
	mu      sync.Mutex
	on      bool
	fresh   time.Duration
	max     int
	now     func() time.Time
	bg      context.Context
	entries map[rowKey]*rowEntry
	byLib   map[string]map[int]*rowEntry
	lru     *list.List // front: the most recently used
	// reads counts the database reads (tests)
	reads int
}

// NewRowCache makes a cache whose background reads end with ctx.
func NewRowCache(ctx context.Context) *RowCache {
	return &RowCache{fresh: rowFresh, max: rowEntries, now: time.Now, bg: ctx,
		entries: map[rowKey]*rowEntry{}, byLib: map[string]map[int]*rowEntry{}, lru: list.New()}
}

// Resume drops every entry and starts keeping them (the listener connected).
func (c *RowCache) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropAll()
	c.on = true
}

// Suspend drops every entry and keeps none until Resume (the listener lost
// its connection, so a change could go unheard).
func (c *RowCache) Suspend() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropAll()
	c.on = false
}

// Invalidate drops lib's entries ("*": every library's).
func (c *RowCache) Invalidate(lib string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lib == "*" {
		c.dropAll()
		return
	}
	for _, e := range c.byLib[lib] {
		c.drop(e)
	}
}

func (c *RowCache) dropAll() {
	for _, e := range c.entries {
		c.drop(e)
	}
}

// drop removes e; the caller holds mu.
func (c *RowCache) drop(e *rowEntry) {
	e.dead = true
	delete(c.entries, e.key)
	if m := c.byLib[e.key.lib]; m != nil {
		delete(m, e.key.n)
		if len(m) == 0 {
			delete(c.byLib, e.key.lib)
		}
	}
	c.lru.Remove(e.elem)
}

// add makes the entry for k; the caller holds mu. Past the cap, the least
// recently used entry goes (never one being read for the first time: its
// callers wait on it).
func (c *RowCache) add(k rowKey) *rowEntry {
	e := &rowEntry{key: k}
	e.elem = c.lru.PushFront(e)
	c.entries[k] = e
	if c.byLib[k.lib] == nil {
		c.byLib[k.lib] = map[int]*rowEntry{}
	}
	c.byLib[k.lib][k.n] = e
	for el := c.lru.Back(); c.lru.Len() > c.max && el != nil; {
		old := el.Value.(*rowEntry)
		el = el.Prev()
		if old.has {
			c.drop(old)
		}
	}
	return e
}

// rows is up to n of the newest visible videos of each of libs: from the
// entries, reading the missing ones (one query for them all) and, in the
// background, the stale ones.
func (c *RowCache) rows(ctx context.Context, libs []string, n int, load rowLoader) (map[string][]Card, error) {
	c.mu.Lock()
	if !c.on {
		c.mu.Unlock()
		return load(ctx, libs, n)
	}
	out := make(map[string][]Card, len(libs))
	var cold, stale []*rowEntry
	var wait []*rowEntry
	now := c.now()
	for _, lib := range libs {
		k := rowKey{lib, n}
		e := c.entries[k]
		switch {
		case e == nil:
			e = c.add(k)
			cold = append(cold, e)
		case !e.has:
			wait = append(wait, e) // another caller is reading it
		default:
			c.lru.MoveToFront(e.elem)
			out[lib] = e.cards
			if e.read == nil && now.Sub(e.at) >= c.fresh {
				stale = append(stale, e)
			}
		}
	}
	read := func(es []*rowEntry) *rowRead {
		r := &rowRead{done: make(chan struct{})}
		for _, e := range es {
			e.read = r
		}
		return r
	}
	var coldRead, staleRead *rowRead
	if len(cold) > 0 {
		coldRead = read(cold)
	}
	if len(stale) > 0 {
		staleRead = read(stale)
	}
	waits := make([]*rowRead, len(wait))
	for i, e := range wait {
		waits[i] = e.read
	}
	c.mu.Unlock()

	if staleRead != nil {
		go func() {
			defer safego.Recover(nil, "browse_rowcache_refresh")
			ctx, cancel := context.WithTimeout(c.bg, rowReadTimeout)
			defer cancel()
			c.fill(ctx, stale, staleRead, n, load)
		}()
	}
	if coldRead != nil {
		c.fill(ctx, cold, coldRead, n, load)
		if coldRead.err != nil {
			return nil, coldRead.err
		}
		for _, e := range cold {
			out[e.key.lib] = nonNil(coldRead.got[e.key.lib])
		}
	}
	for i, e := range wait {
		r := waits[i]
		select {
		case <-r.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if r.err != nil {
			// the other caller's read failed: read this one ourselves
			m, err := load(ctx, []string{e.key.lib}, n)
			if err != nil {
				return nil, err
			}
			out[e.key.lib] = nonNil(m[e.key.lib])
			continue
		}
		out[e.key.lib] = nonNil(r.got[e.key.lib])
	}
	return out, nil
}

// fill reads es's libraries into r and keeps the answer in every entry not
// dropped meanwhile. A failed first read drops its entries; a failed
// background read keeps the old cards (the next request tries again).
func (c *RowCache) fill(ctx context.Context, es []*rowEntry, r *rowRead, n int, load rowLoader) {
	libs := make([]string, len(es))
	for i, e := range es {
		libs[i] = e.key.lib
	}
	at := c.now() // the answer is as old as the read's start
	m, err := load(ctx, libs, n)
	c.mu.Lock()
	c.reads++
	r.got, r.err = m, err
	for _, e := range es {
		if e.read == r {
			e.read = nil
		}
		if e.dead {
			continue
		}
		if err != nil {
			if !e.has {
				c.drop(e)
			}
			continue
		}
		e.cards, e.at, e.has = nonNil(m[e.key.lib]), at, true
	}
	c.mu.Unlock()
	close(r.done)
}

func nonNil(cs []Card) []Card {
	if cs == nil {
		return []Card{}
	}
	return cs
}

// Listen keeps the cache in step with the database until ctx ends: it
// LISTENs on LibraryChangedChannel over dsn (a connection of its own) and
// drops a library's entries when its videos change. The cache keeps nothing
// until the listener is connected, nor while it is not. resettle, when set,
// gives the connection string again after a failed connection (as the event
// bus's listener: pgdsn.ForLibPQ); the listener is made anew when it changed.
func (c *RowCache) Listen(ctx context.Context, dsn string, resettle func(ctx context.Context) string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	defer safego.Recover(nil, "browse_rowcache_listen")
	defer c.Suspend()
	for ctx.Err() == nil {
		failing := make(chan struct{}, 1)
		l := pq.NewListener(dsn, 10*time.Second, time.Minute, func(ev pq.ListenerEventType, err error) {
			switch ev {
			case pq.ListenerEventReconnected:
				// resync has listened again; what came meanwhile is unknown
				c.Resume()
			case pq.ListenerEventDisconnected, pq.ListenerEventConnectionAttemptFailed:
				c.Suspend()
				if err != nil {
					log.Warn("browse: row cache listener lost the database; rows read directly",
						"err", err, "event", "browse_rowcache_listener_error")
				}
				select {
				case failing <- struct{}{}:
				default:
				}
			}
		})
		listened := make(chan error, 1)
		go func() { listened <- l.Listen(LibraryChangedChannel) }()
		next := c.listenOn(ctx, l, listened, failing, dsn, resettle, log)
		_ = l.Close()
		if next == dsn {
			return
		}
		log.Info("browse: the row cache listener's connection was settled again",
			"event", "browse_rowcache_listener_resettled")
		dsn = next
	}
}

// listenOn serves l until ctx ends (it returns dsn) or the connection string
// must change (it returns the new one).
func (c *RowCache) listenOn(ctx context.Context, l *pq.Listener, listened <-chan error, failing <-chan struct{},
	dsn string, resettle func(ctx context.Context) string, log *slog.Logger) string {
	for {
		select {
		case <-ctx.Done():
			return dsn
		case err := <-listened:
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("browse: row cache listener could not listen; rows read directly",
						"err", err, "event", "browse_rowcache_listen_failed")
				}
				return dsn
			}
			c.Resume()
		case <-failing:
			if resettle == nil {
				continue
			}
			if next := resettle(ctx); next != dsn && ctx.Err() == nil {
				return next
			}
		case n := <-l.Notify:
			if n == nil {
				continue // a reconnect: Resume ran from the event
			}
			c.Invalidate(n.Extra)
		}
	}
}
