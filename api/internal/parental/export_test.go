package parental

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// What the package's external tests need of the cache's inside.

const (
	EntryMaxAge  = entryMaxAge
	UnlimitedFor = unlimitedFor
	ReadTimeout  = readTimeout
)

// SetClock gives the cache a clock.
func (c *Cache) SetClock(now func() time.Time) { c.now = now }

// SetAfterRead runs f in For between the read of a limit and the note of it.
func (c *Cache) SetAfterRead(f func()) { c.afterRead = f }

// Resume makes the cache keep entries, as a connected listener does.
func (c *Cache) Resume() { c.resume() }

// Suspend is what a lost listener does.
func (c *Cache) Suspend() { c.suspend() }

// SetPing sets how often the listener is pinged and how long it may take; it
// returns the function that puts the old values back.
func SetPing(every, timeout time.Duration) func() {
	oe, ot := pingEvery, pingTimeout
	pingEvery, pingTimeout = every, timeout
	return func() { pingEvery, pingTimeout = oe, ot }
}

// FakeListener stands for one pq.Listener of the cache: the test sends its events
// and answers its pings.
type FakeListener struct {
	ls       *listenState
	listened chan error
	// Done is closed when the cache let the listener go; Again says it asked for
	// a new one.
	Done  chan struct{}
	Again bool
}

// ServeFake runs the cache's listener loop over a listener whose pings are ping.
func (c *Cache) ServeFake(ctx context.Context, ping func() error) *FakeListener {
	f := &FakeListener{ls: &listenState{c: c}, listened: make(chan error, 1), Done: make(chan struct{})}
	go func() {
		defer close(f.Done)
		_, f.Again = c.listenOn(ctx, f.ls, ping, make(chan *pq.Notification), f.listened, make(chan struct{}), "dsn", nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		f.ls.end()
	}()
	return f
}

// Event is one of the listener's events, as pq's callback gives it.
func (f *FakeListener) Event(ev pq.ListenerEventType) { f.ls.event(ev) }

// Listened says the LISTEN was issued.
func (f *FakeListener) Listened() { f.listened <- nil }
