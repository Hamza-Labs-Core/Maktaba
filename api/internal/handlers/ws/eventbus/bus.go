package eventbus

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// Retention defaults (Story 19.2 AC3: 7-day durable replay window).
// Exported so main.go / the router wiring use one canonical value.
const (
	DefaultRetention  = 7 * 24 * time.Hour
	DefaultPruneEvery = time.Hour
)

// Bus is one API replica's view of the cross-replica event bus. It
// owns the local ws.Hub fan-out and one LISTEN loop. Construct one per
// replica with the shared Backend; Publish from any handler, Run once
// from main.go.
type Bus struct {
	backend Backend
	hub     *ws.Hub
	log     *slog.Logger

	// mu guards lastID: the highest event id this replica has already
	// fanned out, per channel. It drives gap recovery — a NOTIFY for
	// id N triggers a replay of (lastID, N] so a dropped NOTIFY (queue
	// overflow, brief disconnect) never silently loses an event.
	mu     sync.Mutex
	lastID map[string]int64
}

// New wires a Bus to backend and the replica-local hub. logger nil →
// slog.Default() (mirrors the idempotency.PostgresStore fallback).
func New(backend Backend, hub *ws.Hub, logger *slog.Logger) *Bus {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bus{
		backend: backend,
		hub:     hub,
		log:     logger,
		lastID:  map[string]int64{},
	}
}

// Publish appends the event to the durable log. Fan-out is NOT done
// here directly: the row insert fires the NOTIFY, every replica's Run
// loop (including this one) reads it back and fans it out to its local
// hub. Routing through the table is what makes a 2nd replica's clients
// receive the event, and what makes the event replayable.
func (b *Bus) Publish(ctx context.Context, channel, typ string, payload map[string]any) error {
	_, err := b.backend.Append(ctx, Event{
		Channel: channel,
		Type:    typ,
		Payload: payload,
		At:      time.Now().UTC(),
	})
	if err != nil {
		b.log.Warn("eventbus: append failed; event not fanned out",
			"err", err, "channel", channel, "type", typ,
			"event", "eventbus_append_failed")
	}
	return err
}

// Run is the per-replica LISTEN loop. It blocks until ctx is done. For
// every NOTIFY it reads the full row back (the NOTIFY is bounded to
// id+channel) and, crucially, replays any gap (lastID, id] before
// delivering id — so an overflowed/dropped NOTIFY self-heals on the
// next one. Call once per replica from main.go in a goroutine.
func (b *Bus) Run(ctx context.Context) error {
	notes, err := b.backend.Listen(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-notes:
			if !ok {
				return nil
			}
			b.deliverThrough(ctx, n.Channel, n.ID)
		}
	}
}

// deliverThrough fans out every not-yet-delivered event on channel up
// to and including upTo. It always re-reads from the table starting
// after the last id this replica delivered, so a NOTIFY that was never
// received still gets caught here (the gap-recovery property).
func (b *Bus) deliverThrough(ctx context.Context, channel string, upTo int64) {
	b.mu.Lock()
	from := b.lastID[channel]
	b.mu.Unlock()

	if upTo <= from {
		return // already delivered (duplicate NOTIFY / out-of-order)
	}

	evs, err := b.backend.Replay(ctx, channel, from)
	if err != nil {
		b.log.Warn("eventbus: replay during fan-out failed; will retry on next notify",
			"err", err, "channel", channel, "from", from,
			"event", "eventbus_replay_failed")
		return
	}
	var maxID int64 = from
	for _, e := range evs {
		if e.ID > upTo {
			// Beyond what this NOTIFY announced; a later NOTIFY for
			// that id will deliver it (keeps ordering tight).
			break
		}
		b.hub.Publish(e.Channel, e.toHubEvent())
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	b.mu.Lock()
	if maxID > b.lastID[channel] {
		b.lastID[channel] = maxID
	}
	b.mu.Unlock()
}

// Replay returns every event on channel with id > lastEventID, for the
// on-(re)connect catch-up handshake (Story 19.2 AC3). A client passes
// the "_event_id" of the last event it processed; this returns the
// gap. lastEventID 0 → full history still in the table (post-prune).
func (b *Bus) Replay(ctx context.Context, channel string, lastEventID int64) ([]Event, error) {
	return b.backend.Replay(ctx, channel, lastEventID)
}

// Pruner runs Backend.Prune on a ticker until ctx is done, enforcing
// the 7-day retention (Story 19.2 AC3). Call once cluster-wide-ish
// from main.go; concurrent pruners are safe (DELETE is idempotent).
func (b *Bus) Pruner(ctx context.Context, retention, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := b.backend.Prune(ctx, time.Now().Add(-retention))
			if err != nil {
				b.log.Warn("eventbus: prune failed",
					"err", err, "event", "eventbus_prune_failed")
				continue
			}
			if n > 0 {
				b.log.Info("eventbus: pruned expired events",
					"count", n, "retention", retention.String())
			}
		}
	}
}
