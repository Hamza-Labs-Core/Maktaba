// Package pqlisten is the components_changed listener for processes on lib/pq (the API,
// the supervisor): a pq.Listener, which reconnects by itself with a 1-30 s backoff and
// tells of each reconnect, sent on as a Reload event so a notification lost while the
// connection was down never leaves a runner stale.
package pqlisten

import (
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// PingEvery is how often an idle connection is checked (a dead one reconnects).
const PingEvery = 90 * time.Second

// Listener is a reconnecting components_changed listener.
type Listener struct {
	pq     *pq.Listener
	events chan store.Event
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

// New listens on store.Channel at dsn. onError (may be nil) hears connection problems.
func New(dsn string, onError func(error)) (*Listener, error) {
	cb := func(ev pq.ListenerEventType, err error) {
		if err != nil && onError != nil {
			onError(err)
		}
	}
	pl := pq.NewListener(dsn, time.Second, 30*time.Second, cb)
	if err := pl.Listen(store.Channel); err != nil {
		_ = pl.Close()
		return nil, err
	}
	l := &Listener{pq: pl, events: make(chan store.Event, 16), stop: make(chan struct{}), done: make(chan struct{})}
	go l.run()
	return l, nil
}

func (l *Listener) run() {
	defer close(l.done)
	defer close(l.events)
	ping := time.NewTicker(PingEvery)
	defer ping.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ping.C:
			_ = l.pq.Ping()
		case n, ok := <-l.pq.Notify:
			if !ok {
				return
			}
			ev := store.Event{Reload: true} // nil: the connection was re-established
			if n != nil {
				ev = store.ParseNotify(n.Extra)
			}
			select {
			case l.events <- ev:
			case <-l.stop:
				return
			}
		}
	}
}

// Events implements store.Listener.
func (l *Listener) Events() <-chan store.Event { return l.events }

// Close stops listening and closes the connection.
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.stop)
		err = l.pq.Close()
		<-l.done
	})
	return err
}
