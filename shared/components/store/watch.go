package store

import (
	"context"
	"encoding/json"
	"time"
)

// DefaultPoll is how often a Watcher asks the revision when no notification comes.
const DefaultPoll = 30 * time.Second

// Event is one components_changed notification, or (Reload) a listener's (re)connect,
// after which a notification may have been missed.
type Event struct {
	Revision int64  `json:"revision"`
	ID       string `json:"id"`
	Node     string `json:"node"`
	Reload   bool   `json:"-"`
}

// ParseNotify reads a components_changed payload; one it cannot read is a Reload.
func ParseNotify(payload string) Event {
	var e struct {
		Revision int64   `json:"revision"`
		ID       string  `json:"id"`
		Node     *string `json:"node"`
	}
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return Event{Reload: true}
	}
	ev := Event{Revision: e.Revision, ID: e.ID}
	if e.Node != nil {
		ev.Node = *e.Node
	}
	return ev
}

// Listener delivers components_changed notifications. It reconnects by itself and
// sends an Event with Reload after every reconnect (store/pqlisten, store/pgxlisten).
type Listener interface {
	Events() <-chan Event
	Close() error
}

// Source is what a Watcher reads: *Store is one.
type Source interface {
	Revision(ctx context.Context) (int64, error)
	Desired(ctx context.Context, machine string) (map[string]Desired, int64, error)
}

// Watcher hands a runner the desired state of its machine whenever it changes. Level
// based: it loads at start, then reloads when a notification names a revision other
// than the one it holds, after a listener's reconnect, or when the revision it polls
// (every Poll) moved. When nothing changes it costs one revision query per Poll (N12).
// With no Listener (SQLite) it only polls.
type Watcher struct {
	Source   Source
	Machine  string
	Listener Listener
	Poll     time.Duration // 0: DefaultPoll
	// After is the timer (nil: time.After); tests give a fake clock.
	After func(time.Duration) <-chan time.Time
	// OnError hears a failed read; the Watcher keeps going (nil: ignored).
	OnError func(error)
}

// Run calls apply with every new desired state until ctx ends (its error is returned).
// apply runs on Run's goroutine; a slow apply delays the next look, never overlaps it.
func (w *Watcher) Run(ctx context.Context, apply func(desired map[string]Desired, revision int64)) error {
	poll := w.Poll
	if poll <= 0 {
		poll = DefaultPoll
	}
	after := w.After
	if after == nil {
		after = time.After
	}
	fail := func(err error) {
		if w.OnError != nil && err != nil && ctx.Err() == nil {
			w.OnError(err)
		}
	}
	last := int64(-1)
	load := func() {
		d, rev, err := w.Source.Desired(ctx, w.Machine)
		if err != nil {
			fail(err)
			return
		}
		last = rev
		apply(d, rev)
	}
	var events <-chan Event
	if w.Listener != nil {
		events = w.Listener.Events()
	}
	load()
	// one timer at a time: a notification does not push the next poll back
	tick := after(poll)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				events = nil // the listener closed: poll only
				continue
			}
			if ev.Reload || ev.Revision != last {
				load()
			}
		case <-tick:
			tick = after(poll)
			rev, err := w.Source.Revision(ctx)
			if err != nil {
				fail(err)
				continue
			}
			if rev != last {
				load()
			}
		}
	}
}
