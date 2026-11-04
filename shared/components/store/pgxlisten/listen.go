// Package pgxlisten is the components_changed listener for processes on pgx
// (streaming): one dedicated connection that LISTENs, reconnecting with a 1-30 s backoff
// and sending a Reload event after every (re)connect, so a notification lost while the
// connection was down never leaves a runner stale.
package pgxlisten

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Conn is the part of a *pgx.Conn the listener uses.
type Conn interface {
	Listen(ctx context.Context, channel string) error
	WaitForNotification(ctx context.Context) (*pgconn.Notification, error)
	Close(ctx context.Context) error
}

type pgxConn struct{ c *pgx.Conn }

func (p pgxConn) Listen(ctx context.Context, channel string) error {
	_, err := p.c.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize())
	return err
}

func (p pgxConn) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	return p.c.WaitForNotification(ctx)
}

func (p pgxConn) Close(ctx context.Context) error { return p.c.Close(ctx) }

// Dial connects to dsn with pgx.
func Dial(dsn string) func(ctx context.Context) (Conn, error) {
	return func(ctx context.Context) (Conn, error) {
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return nil, err
		}
		return pgxConn{c}, nil
	}
}

// Listener is a reconnecting components_changed listener.
type Listener struct {
	events chan store.Event
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// Options tune the listener; the zero value is the production one.
type Options struct {
	MinBackoff, MaxBackoff time.Duration // 0: 1 s and 30 s
	// Sleep waits d or until ctx ends (nil: a timer); tests make it instant.
	Sleep func(ctx context.Context, d time.Duration)
	// OnError hears a failed connect or a dropped connection (nil: ignored).
	OnError func(error)
}

// New starts listening on store.Channel through connect.
func New(connect func(ctx context.Context) (Conn, error), opt Options) *Listener {
	if opt.MinBackoff <= 0 {
		opt.MinBackoff = time.Second
	}
	if opt.MaxBackoff <= 0 {
		opt.MaxBackoff = 30 * time.Second
	}
	if opt.Sleep == nil {
		opt.Sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Listener{events: make(chan store.Event, 16), cancel: cancel, done: make(chan struct{})}
	go l.run(ctx, connect, opt)
	return l
}

func (l *Listener) run(ctx context.Context, connect func(ctx context.Context) (Conn, error), opt Options) {
	defer close(l.done)
	defer close(l.events)
	fail := func(err error) {
		if opt.OnError != nil && ctx.Err() == nil {
			opt.OnError(err)
		}
	}
	backoff := opt.MinBackoff
	for ctx.Err() == nil {
		c, err := connect(ctx)
		if err == nil {
			if err = c.Listen(ctx, store.Channel); err != nil {
				_ = c.Close(context.Background())
			}
		}
		if err != nil {
			fail(err)
			opt.Sleep(ctx, backoff)
			backoff = min(backoff*2, opt.MaxBackoff)
			continue
		}
		backoff = opt.MinBackoff
		if !l.send(ctx, store.Event{Reload: true}) {
			_ = c.Close(context.Background())
			return
		}
		for {
			n, err := c.WaitForNotification(ctx)
			if err != nil {
				fail(err)
				break
			}
			if !l.send(ctx, store.ParseNotify(n.Payload)) {
				break
			}
		}
		_ = c.Close(context.Background())
		if ctx.Err() == nil {
			opt.Sleep(ctx, backoff)
		}
	}
}

func (l *Listener) send(ctx context.Context, e store.Event) bool {
	select {
	case l.events <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// Events implements store.Listener.
func (l *Listener) Events() <-chan store.Event { return l.events }

// Close stops listening and closes the connection.
func (l *Listener) Close() error {
	l.once.Do(func() {
		l.cancel()
		<-l.done
	})
	return nil
}
