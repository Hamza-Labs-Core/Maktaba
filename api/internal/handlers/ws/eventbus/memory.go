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

// Replay returns every row on channel with id > afterID, ascending.
func (m *MemoryBackend) Replay(_ context.Context, channel string, afterID int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.rows {
		if e.Channel == channel && e.ID > afterID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
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
