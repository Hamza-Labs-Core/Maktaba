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
type Cache struct {
	store *Store

	mu      sync.Mutex
	on      bool
	gen     uint64
	entries map[string]*Effective
	// signals are the sign-ins' device signals (signal.go), dropped with the
	// account's entry.
	signals map[signalKey]*SignInSignal
}

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

// For returns the effective policy of the request's principal: the account's
// policy, held as well to the device limit of the sign-in the request came through
// (story 57.04), whichever is stricter. No principal, or one with no user id (the
// admin token), is unrestricted.
func (c *Cache) For(ctx context.Context, p *principal.Principal) (*Effective, error) {
	if p == nil {
		return &Effective{}, nil
	}
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

// Invalidate drops userID's entry ("": every entry).
func (c *Cache) Invalidate(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.dropSignals(userID)
	if userID == "" {
		c.entries = map[string]*Effective{}
		return
	}
	delete(c.entries, userID)
}

// resume starts keeping entries (the listener is connected and listening).
func (c *Cache) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.entries = map[string]*Effective{}
	c.signals = nil
	c.on = true
}

// suspend drops every entry and keeps none (the listener lost its connection, so
// a change could go unheard).
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
		next := c.listenOn(ctx, l, listened, failing, dsn, resettle, log)
		_ = l.Close()
		if next == dsn {
			return
		}
		dsn = next
	}
}

// listenOn serves l until ctx ends (it returns dsn) or the connection string must
// change (it returns the new one).
func (c *Cache) listenOn(ctx context.Context, l *pq.Listener, listened <-chan error, failing <-chan struct{},
	dsn string, resettle func(ctx context.Context) string, log *slog.Logger) string {
	for {
		select {
		case <-ctx.Done():
			return dsn
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
		case n := <-l.Notify:
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
