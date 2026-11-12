package parental

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Cache keeps each account's effective policy in memory so a request reads it
// without a query. It holds entries only while its listener is connected to
// NOTIFY parental_policy: a change by any API process drops the child's entry, so
// a token minted before the change sees the new limit on its next request. With no
// listener (or while it is reconnecting) every read goes to the database.
//
// The listener's connection can die without saying so (the home asleep, the link
// dropped): the cache pings it every pingEvery; a ping that fails or does not
// answer suspends the cache and the listener is made anew. A ping never resumes
// it: only the listener's own word that it is connected and listening does
// (listenState). And no entry is trusted for longer than entryMaxAge whatever the
// listener says.
//
// A read that fails refuses (fail closed), with one bounded exception: a sign-in
// whose last answer, read from the database no longer than unlimitedFor ago, was
// "no limit at all" (no policy and no device signal) is answered that again (the
// note For writes; For says in what order, and why a heard change leaves none). On a node whose home
// database is down, an account nothing restricts keeps playing what the node
// holds; an account with a policy, a sign-in with a signal, and one never read
// are refused.
type Cache struct {
	store *Store
	// now is the clock (nil: time.Now); a test sets it.
	now func() time.Time
	// afterRead, when a test sets it, runs in For between the read and the note.
	afterRead func()

	mu      sync.Mutex
	on      bool
	gen     uint64
	since   time.Time // when the oldest entry kept now may have been read
	entries map[string]*Effective
	// signals are the sign-ins' device signals (signal.go), dropped with the
	// account's entry.
	signals map[signalKey]*SignInSignal
	// unlimited is when each sign-in was last answered "no limit at all".
	unlimited map[signInKey]time.Time
}

// signInKey names one sign-in as a limit sees it: the account, the sign-in's
// family and the limit its token claims.
type signInKey struct {
	userID, family string
	claim          int
}

const (
	// readTimeout bounds one read of a request's limit: a database that does not
	// answer refuses the request (or answers from unlimited) instead of holding it.
	readTimeout = 3 * time.Second
	// unlimitedFor is how long a "no limit at all" note stands in for a read that
	// fails, counted from when the database read it came from began.
	unlimitedFor = time.Minute
	// entryMaxAge is the longest an entry is trusted without being read again.
	entryMaxAge = time.Minute
)

// pingEvery and pingTimeout are how often the listener's connection is checked and
// how long its answer may take (variables: a test shortens them).
var (
	pingEvery   = 15 * time.Second
	pingTimeout = 5 * time.Second
)

// maxCached bounds the map: past it the cache starts over (a home has few users;
// this only stops a flood of unknown ids from growing it).
const maxCached = 4096

// NewCache returns a cache over store that reads through until Listen connects.
func NewCache(store *Store) *Cache {
	return &Cache{store: store, entries: map[string]*Effective{}}
}

// Store is the store the cache reads from.
func (c *Cache) Store() *Store { return c.store }

// Effective returns userID's effective policy. The value is shared: callers must
// not change it.
func (c *Cache) Effective(ctx context.Context, userID string) (*Effective, error) {
	e, _, err := c.effective(ctx, userID)
	return e, err
}

// effective is Effective, and whether the answer is a kept entry (not read from
// the database in this call).
func (c *Cache) effective(ctx context.Context, userID string) (*Effective, bool, error) {
	c.mu.Lock()
	c.expire()
	if e, ok := c.entries[userID]; ok {
		c.mu.Unlock()
		return e, true, nil
	}
	gen := c.gen
	c.mu.Unlock()

	e, err := c.store.Effective(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	// kept only when nothing changed while it was read: a notification in between
	// may have been for this user
	if c.on && c.gen == gen {
		if len(c.entries) >= maxCached {
			c.entries = map[string]*Effective{}
		}
		c.entries[userID] = e
	}
	c.mu.Unlock()
	return e, false, nil
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// expire starts the cache over once what it keeps may be entryMaxAge old: every
// entry and signal is then read again, so none outlives that age even when a
// change went unheard. The caller holds c.mu.
func (c *Cache) expire() {
	now := c.clock()
	if c.since.IsZero() || now.Before(c.since) {
		c.since = now
		return
	}
	if now.Sub(c.since) < entryMaxAge {
		return
	}
	c.since = now
	if len(c.entries) == 0 && len(c.signals) == 0 {
		return
	}
	c.gen++
	c.entries = map[string]*Effective{}
	c.signals = nil
}

// For returns the effective policy of the request's principal: the account's
// policy, held as well to the device limit of the sign-in the request came through
// (story 57.04), whichever is stricter. No principal, or one with no user id (the
// admin token), is unrestricted.
//
// The read is bounded by readTimeout. When it fails, a sign-in with a live "no
// limit at all" note is answered that (see Cache). The note is written in this
// order, so it cannot stand for a state older than a change this cache heard:
//
//  1. the generation is read, then the clock (the note's stamp is from before the
//     read began, never after);
//  2. the policy and the signal are read;
//  3. the note is written only if every part of the answer came from the database
//     in this call (an answer from a kept entry never renews a note), the answer
//     has no limit, and the generation has not moved since step 1. Invalidate
//     moves the generation and drops the account's notes under the same lock, so
//     a change heard at any moment after step 1 leaves no note: either the write
//     sees the moved generation, or the drop comes after the write.
//
// A change this cache never hears (its listener is down) is bounded by the note's
// age alone: unlimitedFor after a read that began before the change.
func (c *Cache) For(ctx context.Context, p *principal.Principal) (*Effective, error) {
	if p == nil {
		return &Effective{}, nil
	}
	key := signInKey{p.UserID, p.RefreshFamily, ClaimLimit(p.AgeCeiling)}
	c.mu.Lock()
	c.expire()
	gen := c.gen
	began := c.clock()
	c.mu.Unlock()

	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	e, fresh, err := c.read(rctx, p)
	if err != nil {
		if ctx.Err() == nil && c.wasUnlimited(key) {
			// the limit cannot be read, and this sign-in had none a moment ago
			return &Effective{UserID: p.UserID}, nil
		}
		return nil, err
	}
	if c.afterRead != nil {
		c.afterRead()
	}
	c.noteUnlimited(key, gen, began, fresh, !e.Limits())
	return e, nil
}

// wasUnlimited reports whether key has a live "no limit at all" note: written
// after a database read that began no longer than unlimitedFor ago.
func (c *Cache) wasUnlimited(key signInKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.unlimited[key]
	if !ok {
		return false
	}
	if age := c.clock().Sub(at); age < 0 || age > unlimitedFor {
		delete(c.unlimited, key)
		return false
	}
	return true
}

// noteUnlimited records what For just answered key (see For for the order). An
// answer with a limit drops the note whatever it was read from. An answer with
// none writes it, stamped began, only when it was read from the database in this
// call and nothing was invalidated since gen was read; an answer from a kept
// entry leaves the note as it is, to age out.
func (c *Cache) noteUnlimited(key signInKey, gen uint64, began time.Time, fresh, unlimited bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !unlimited {
		delete(c.unlimited, key)
		return
	}
	if !fresh || c.gen != gen {
		return
	}
	if c.unlimited == nil || len(c.unlimited) >= maxCached {
		c.unlimited = map[signInKey]time.Time{}
	}
	c.unlimited[key] = began
}

// dropUnlimited forgets userID's sign-ins ("": everyone's). The caller holds c.mu.
func (c *Cache) dropUnlimited(userID string) {
	if userID == "" {
		c.unlimited = nil
		return
	}
	for k := range c.unlimited {
		if k.userID == userID {
			delete(c.unlimited, k)
		}
	}
}

// read is the limit of p, and whether every part of it was read from the database
// in this call (none from a kept entry).
func (c *Cache) read(ctx context.Context, p *principal.Principal) (*Effective, bool, error) {
	fresh := true
	e := &Effective{}
	if isUUID(p.UserID) {
		var err error
		var kept bool
		if e, kept, err = c.effective(ctx, p.UserID); err != nil {
			return nil, false, err
		}
		fresh = !kept
	}
	if c.signalKept(p) {
		fresh = false
	}
	sig, err := c.signalFor(ctx, p)
	if err != nil {
		return nil, false, err
	}
	if sig == nil {
		return e, fresh, nil
	}
	return e.WithSignal(*sig), fresh, nil
}

// signalKept reports whether the signal signalFor will read for p is a kept one
// (the key is signalFor's).
func (c *Cache) signalKept(p *principal.Principal) bool {
	family := p.RefreshFamily
	if !isUUID(p.UserID) || !isUUID(family) {
		family = ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.signals[signalKey{p.UserID, family}]
	return ok
}

// Generation changes whenever an entry was dropped or the cache started over: a
// caller that kept an answer of its own asks again when it differs.
func (c *Cache) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// Invalidate drops userID's entry ("": every entry).
func (c *Cache) Invalidate(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.dropSignals(userID)
	// a change was heard: what the account was answered before says nothing now
	c.dropUnlimited(userID)
	if userID == "" {
		c.entries = map[string]*Effective{}
		return
	}
	delete(c.entries, userID)
}

// resume starts keeping entries (the listener is connected and listening). What
// changed while it was not is unknown, so nothing answered before stands.
func (c *Cache) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.entries = map[string]*Effective{}
	c.signals = nil
	c.unlimited = nil
	c.since = c.clock()
	c.on = true
}

// suspend drops every entry and keeps none (the listener lost its connection, so
// a change could go unheard). The "no limit at all" answers stay for their short
// life: they are for exactly the time the database cannot be reached.
func (c *Cache) suspend() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.entries = map[string]*Effective{}
	c.signals = nil
	c.on = false
}

// listenState is what one listener has said about itself, and the one place the
// cache is resumed. The cache keeps entries only while both hold: the listener's
// last event says its connection is up, and its LISTEN was issued. Every change
// goes through set, under one lock, so the cache's state follows the listener's
// events in the order they came: a disconnect is never followed by a resume that
// was decided before it. A listener that was let go (dead) changes nothing.
type listenState struct {
	c *Cache

	mu        sync.Mutex
	dead      bool
	up        bool // the last event: connected (or reconnected), not lost since
	listening bool // Listen returned with no error
}

// event takes one of the listener's events. It reports whether the connection was
// lost.
func (ls *listenState) event(ev pq.ListenerEventType) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.dead {
		return false
	}
	switch ev {
	case pq.ListenerEventConnected, pq.ListenerEventReconnected:
		// it is connected and listens again (pq issues every LISTEN again before
		// it says so); what changed meanwhile is unknown, so resume starts over
		ls.up = true
		if ls.listening {
			ls.c.resume()
		}
	case pq.ListenerEventDisconnected, pq.ListenerEventConnectionAttemptFailed:
		ls.up = false
		ls.c.suspend()
		return true
	}
	return false
}

// listened says the LISTEN was issued.
func (ls *listenState) listened() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.dead {
		return
	}
	ls.listening = true
	if ls.up {
		ls.c.resume()
	}
}

// silent says the listener did not answer a ping: the cache keeps nothing, and
// nothing this listener says later is believed (it is replaced).
func (ls *listenState) silent() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.dead = true
	ls.up = false
	ls.c.suspend()
}

// end lets the listener go without a word about the cache.
func (ls *listenState) end() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.dead = true
}

// Listen keeps the cache in step with the database until ctx ends: it LISTENs on
// NotifyChannel over dsn (a connection of its own). resettle, when set, gives the
// connection string again after a failed connection (as the row cache's listener);
// the listener is made anew when it changed, and when it did not answer a ping.
func (c *Cache) Listen(ctx context.Context, dsn string, resettle func(ctx context.Context) string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	defer c.suspend()
	for ctx.Err() == nil {
		failing := make(chan struct{}, 1)
		ls := &listenState{c: c}
		l := pq.NewListener(dsn, 10*time.Second, time.Minute, func(ev pq.ListenerEventType, err error) {
			if !ls.event(ev) {
				return
			}
			if err != nil {
				log.Warn("parental: policy cache listener lost the database; policies read directly",
					"err", err, "event", "parental_cache_listener_error")
			}
			select {
			case failing <- struct{}{}:
			default:
			}
		})
		listened := make(chan error, 1)
		go func() { listened <- l.Listen(NotifyChannel) }()
		next, again := c.listenOn(ctx, ls, l.Ping, l.Notify, listened, failing, dsn, resettle, log)
		ls.end()
		// a ping still out holds the listener's lock, and Close waits for it
		go func() { _ = l.Close() }()
		if !again && next == dsn {
			return
		}
		dsn = next
	}
}

// listenOn serves a listener until ctx ends (it returns dsn), the connection
// string must change (it returns the new one), or the listener must be made anew
// (again).
//
// It also checks the connection while the cache keeps entries: ping is asked every
// pingEvery, and one that fails or has not answered within pingTimeout suspends
// the cache (ls.silent) and ends this listener (a connection that died without a
// word would otherwise leave old policies in use until TCP noticed). A ping that
// answers changes nothing: it was sent before whatever happened since, so it can
// never be the reason to keep entries again. The new listener resumes the cache
// when it is connected and listening.
func (c *Cache) listenOn(ctx context.Context, ls *listenState, ping func() error, notify <-chan *pq.Notification,
	listened <-chan error, failing <-chan struct{}, dsn string, resettle func(ctx context.Context) string, log *slog.Logger) (string, bool) {
	tick := time.NewTicker(pingEvery)
	defer tick.Stop()
	var (
		pong    = make(chan error, 1)
		pinging bool             // a ping is out
		late    <-chan time.Time // when the ping that is out is overdue
		timer   *time.Timer
	)
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	silent := func(why string, err error) (string, bool) {
		ls.silent()
		log.Warn("parental: policy cache listener does not answer; policies read directly, the listener is made anew",
			"why", why, "err", err, "event", "parental_cache_listener_silent")
		return dsn, true
	}
	for {
		select {
		case <-ctx.Done():
			return dsn, false
		case <-tick.C:
			if pinging || !c.Listening() {
				continue // one is out, or nothing is kept: the listener's own events say when it is back
			}
			pinging = true
			go func() { pong <- ping() }()
			timer = time.NewTimer(pingTimeout)
			late = timer.C
		case <-late:
			return silent("no answer to a ping", nil)
		case err := <-pong:
			pinging, late = false, nil
			timer.Stop()
			if err != nil && c.Listening() {
				// (while nothing is kept a failed ping says only what the
				// listener's events already said)
				return silent("ping failed", err)
			}
		case err := <-listened:
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("parental: policy cache listener could not listen; policies read directly",
						"err", err, "event", "parental_cache_listen_failed")
				}
				return dsn, false
			}
			ls.listened()
		case <-failing:
			if resettle == nil {
				continue
			}
			if next := resettle(ctx); next != dsn && ctx.Err() == nil {
				return next, false
			}
		case n := <-notify:
			if n == nil {
				continue // a reconnect: resume ran from the event
			}
			c.Invalidate(n.Extra)
		}
	}
}

// Listening reports whether the cache is keeping entries (its listener is
// connected).
func (c *Cache) Listening() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.on
}
