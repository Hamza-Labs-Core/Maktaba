package eventbus

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryBackend models the durable `events` table + the slot-0061
// NOTIFY trigger exactly, with no Postgres. It is NOT a single-host
// shortcut: one MemoryBackend shared by two Bus instances is the
// faithful stand-in for "two API replicas pointed at one Postgres" —
// the Append-row / fan-out-NOTIFY / read-back / replay semantics are
// identical to PostgresBackend, so the cross-replica + replay contract
// is provable at the unit tier (and re-verified against real Postgres
// in the integration tier).
type MemoryBackend struct {
	mu        sync.Mutex
	nextID    int64
	rows      []Event // append-only log, ordered by id
	listeners []chan Notification
	closed    bool
	// listenerDown models a pq.Listener that has lost its connection:
	// the row is still durably appended (the primary committed it) but
	// NO NOTIFY reaches this replica's stream — exactly the DB-blip
	// gap Fix #1's reconnect re-scan must heal. Toggled by
	// SetListenerDown; restored (and recovery proven) by
	// SignalReconnect.
	listenerDown bool
}

// NewMemoryBackend returns an empty backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{}
}

// Append assigns a monotonic id, stores the row, and fans the bounded
// NOTIFY to every registered listener — exactly what the slot-0061
// AFTER INSERT trigger does in Postgres. A listener whose buffer is
// full drops the NOTIFY (modelling Postgres NOTIFY-queue overflow);
// the Bus gap-recovery scan then heals it on the next NOTIFY.
func (m *MemoryBackend) Append(_ context.Context, ev Event) (int64, error) {
	m.mu.Lock()
	m.nextID++
	id := m.nextID
	ev.ID = id
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	m.rows = append(m.rows, ev)
	if m.listenerDown {
		// DB blip: row is durable but this replica's listener is
		// disconnected, so the NOTIFY is not delivered. Recovery is
		// the reconnect re-scan (Fix #1), not "next NOTIFY".
		m.mu.Unlock()
		return id, nil
	}
	ls := append([]chan Notification(nil), m.listeners...)
	m.mu.Unlock()

	n := Notification{ID: id, Channel: ev.Channel}
	for _, ch := range ls {
		select {
		case ch <- n:
		default: // overflow — Bus.deliverThrough recovers via Replay
		}
	}
	return id, nil
}

// Replay returns up to limit rows on channel with id > afterID,
// ascending — modelling the bounded `LIMIT N` query PostgresBackend
// runs (Fix #2). limit <= 0 falls back to DefaultReplayLimit.
func (m *MemoryBackend) Replay(_ context.Context, channel string, afterID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = DefaultReplayLimit
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.rows {
		if e.Channel == channel && e.ID > afterID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetListenerDown toggles the modelled pq.Listener connection state.
// While down, Append still stores the row durably but emits no NOTIFY
// to this replica (the DB-blip gap Fix #1 must heal).
func (m *MemoryBackend) SetListenerDown(down bool) {
	m.mu.Lock()
	m.listenerDown = down
	m.mu.Unlock()
}

// SignalReconnect models a pq.Listener reconnect: it clears the
// down state and pushes the ReconnectSignal sentinel to every
// registered listener. The unit tier uses it to prove Bus.Run's
// bounded-latency recovery re-scan fires on reconnect (Fix #1)
// without a real Postgres blip.
func (m *MemoryBackend) SignalReconnect() {
	m.mu.Lock()
	m.listenerDown = false
	ls := append([]chan Notification(nil), m.listeners...)
	m.mu.Unlock()
	for _, ch := range ls {
		select {
		case ch <- ReconnectSignal:
		default: // listener busy; periodic re-scan tick still covers it
		}
	}
}

// Get reads one row by id.
func (m *MemoryBackend) Get(_ context.Context, id int64) (Event, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.rows {
		if e.ID == id {
			return e, true, nil
		}
	}
	return Event{}, false, nil
}

// Listen registers a fresh NOTIFY stream for one replica. The channel
// is closed when ctx is done or Close is called.
func (m *MemoryBackend) Listen(ctx context.Context) (<-chan Notification, error) {
	ch := make(chan Notification, 256)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		close(ch)
		return ch, nil
	}
	m.listeners = append(m.listeners, ch)
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		m.mu.Lock()
		for i, c := range m.listeners {
			if c == ch {
				m.listeners = append(m.listeners[:i], m.listeners[i+1:]...)
				close(ch)
				break
			}
		}
		m.mu.Unlock()
	}()
	return ch, nil
}

// Prune drops rows older than the cutoff.
func (m *MemoryBackend) Prune(_ context.Context, olderThan time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.rows[:0]
	removed := 0
	for _, e := range m.rows {
		if e.At.Before(olderThan) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.rows = kept
	return removed, nil
}

// Close detaches every listener.
func (m *MemoryBackend) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	for _, ch := range m.listeners {
		close(ch)
	}
	m.listeners = nil
	return nil
}
