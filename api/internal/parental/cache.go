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
// dropped): the cache pings it every pingEvery and keeps nothing while a ping fails
// or does not answer, and no entry is trusted for longer than entryMaxAge whatever
// the listener says.
//
// A read that fails refuses (fail closed), with one bounded exception: a sign-in
// whose last answer, read no longer than unlimitedFor ago, was "no limit at all"
// (no policy and no device signal) is answered that again. On a node whose home
// database is down, an account nothing restricts keeps playing what the node
// holds; an account with a policy, a sign-in with a signal, and one never read
// are refused.
type Cache struct {
	store *Store
	// now is the clock (nil: time.Now); a test sets it.
	now func() time.Time

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
	// unlimitedFor is how long a "no limit at all" answer stands in for a read
	// that fails.
	unlimitedFor = 2 * time.Minute
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
	c.mu.Lock()
	c.expire()
	if e, ok := c.entries[userID]; ok {
		c.mu.Unlock()
		return e, nil
	}
	gen := c.gen
	c.mu.Unlock()

	e, err := c.store.Effective(ctx, userID)
	if err != nil {
		return nil, err
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
	return e, nil
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
func (c *Cache) For(ctx context.Context, p *principal.Principal) (*Effective, error) {
	if p == nil {
		return &Effective{}, nil
	}
	key := signInKey{p.UserID, p.RefreshFamily, ClaimLimit(p.AgeCeiling)}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	e, err := c.read(rctx, p)
	if err != nil {
		if ctx.Err() == nil && c.wasUnlimited(key) {
			// the limit cannot be read, and this sign-in had none a moment ago
			return &Effective{UserID: p.UserID}, nil
		}
		return nil, err
	}
	c.noteUnlimited(key, !e.Limits())
	return e, nil
}

// wasUnlimited reports whether key's last answer was "no limit at all", read no
// longer than unlimitedFor ago.
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

// noteUnlimited keeps (or forgets) that key was just answered "no limit at all".
func (c *Cache) noteUnlimited(key signInKey, unlimited bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !unlimited {
		delete(c.unlimited, key)
		return
	}
	if c.unlimited == nil || len(c.unlimited) >= maxCached {
		c.unlimited = map[signInKey]time.Time{}
	}
	c.unlimited[key] = c.clock()
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

// read is the limit of p as the database (or a kept entry) gives it.
func (c *Cache) read(ctx context.Context, p *principal.Principal) (*Effective, error) {
	c.mu.Lock()
	c.expire()
	c.mu.Unlock()
	e := &Effective{}
	if isUUID(p.UserID) {
		var err error
		if e, err = c.Effective(ctx, p.UserID); err != nil {
			return nil, err
		}
	}
	sig, err := c.signalFor(ctx, p)
	if err != nil {
		return nil, err
	}
	if sig == nil {
		return e, nil
	}
	return e.WithSignal(*sig), nil
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

// Listen keeps the cache in step with the database until ctx ends: it LISTENs on
// NotifyChannel over dsn (a connection of its own). resettle, when set, gives the
// connection string again after a failed connection (as the row cache's listener);
// the listener is made anew when it changed.
func (c *Cache) Listen(ctx context.Context, dsn string, resettle func(ctx context.Context) string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	defer c.suspend()
	for ctx.Err() == nil {
		failing := make(chan struct{}, 1)
		l := pq.NewListener(dsn, 10*time.Second, time.Minute, func(ev pq.ListenerEventType, err error) {
			switch ev {
			case pq.ListenerEventReconnected:
				// it listens again; what changed meanwhile is unknown
				c.resume()
			case pq.ListenerEventDisconnected, pq.ListenerEventConnectionAttemptFailed:
				c.suspend()
				if err != nil {
					log.Warn("parental: policy cache listener lost the database; policies read directly",
						"err", err, "event", "parental_cache_listener_error")
				}
				select {
				case failing <- struct{}{}:
				default:
				}
			}
		})
		listened := make(chan error, 1)
		go func() { listened <- l.Listen(NotifyChannel) }()
		next := c.listenOn(ctx, l.Ping, l.Notify, listened, failing, dsn, resettle, log)
		_ = l.Close()
		if next == dsn {
			return
		}
		dsn = next
	}
}

// listenOn serves l until ctx ends (it returns dsn) or the connection string must
// change (it returns the new one).
//
// It also checks the connection: ping is asked every pingEvery, and while it fails
// or has not answered within pingTimeout the cache keeps nothing (a connection
// that died without a word would otherwise leave old policies in use until TCP
// noticed). A ping that answers again resumes it.
func (c *Cache) listenOn(ctx context.Context, ping func() error, notify <-chan *pq.Notification, listened <-chan error, failing <-chan struct{},
	dsn string, resettle func(ctx context.Context) string, log *slog.Logger) string {
	tick := time.NewTicker(pingEvery)
	defer tick.Stop()
	var (
		pong     = make(chan error, 1)
		pinging  bool             // a ping is out
		late     <-chan time.Time // when the ping that is out is overdue
		unheard  bool             // the cache was suspended by a ping
		lateStop func() bool
	)
	defer func() {
		if lateStop != nil {
			lateStop()
		}
	}()
	lost := func(why string, err error) {
		c.suspend()
		if !unheard {
			log.Warn("parental: policy cache listener does not answer; policies read directly",
				"why", why, "err", err, "event", "parental_cache_listener_silent")
		}
		unheard = true
	}
	for {
		select {
		case <-ctx.Done():
			return dsn
		case <-tick.C:
			if pinging {
				continue // the last one has not answered: already suspended
			}
			pinging = true
			go func() { pong <- ping() }()
			t := time.NewTimer(pingTimeout)
			late, lateStop = t.C, t.Stop
		case <-late:
			late = nil
			if pinging {
				lost("no answer to a ping", nil)
			}
		case err := <-pong:
			pinging, late = false, nil
			if lateStop != nil {
				lateStop()
			}
			switch {
			case err != nil:
				lost("ping failed", err)
			case unheard:
				// it answers again; a real reconnect resumed from its event already
				unheard = false
				c.resume()
			}
		case err := <-listened:
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("parental: policy cache listener could not listen; policies read directly",
						"err", err, "event", "parental_cache_listen_failed")
				}
				return dsn
			}
			c.resume()
		case <-failing:
			if resettle == nil {
				continue
			}
			if next := resettle(ctx); next != dsn && ctx.Err() == nil {
				return next
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
