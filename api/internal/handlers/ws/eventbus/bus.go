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

	// DefaultRescanEvery is the periodic safety re-scan interval
	// (Fix #1): independent of NOTIFY, Bus.Run re-scans every known
	// channel this often so a silently-dropped NOTIFY (no overflow
	// signal, no reconnect) is still recovered with bounded latency.
	// Modest so it is not a hot loop; the upTo<=from guard in
	// deliverThrough makes a tick with nothing new a cheap no-op.
	DefaultRescanEvery = 15 * time.Second
)

// Bus is one API replica's view of the cross-replica event bus. It
// owns the local ws.Hub fan-out and one LISTEN loop. Construct one per
// replica with the shared Backend; Publish from any handler, Run once
// from main.go.
type Bus struct {
	backend Backend
	hub     *ws.Hub
	log     *slog.Logger

	// replayLimit bounds a single Replay/deliverThrough chunk so a
	// long-outage backlog never blocks Bus.Run or OOMs the process
	// (Fix #2). 0 → DefaultReplayLimit. Overridable in tests.
	replayLimit int
	// rescanEvery is the periodic safety re-scan interval (Fix #1).
	// 0 → DefaultRescanEvery. Overridable in tests for a fast tick.
	rescanEvery time.Duration

	// mu guards lastID: the highest event id this replica has already
	// fanned out, per channel. It drives gap recovery — a NOTIFY for
	// id N triggers a replay of (lastID, N] so a dropped NOTIFY (queue
	// overflow, brief disconnect) never silently loses an event. It
	// also serves as the set of "known channels" re-scanned on a
	// listener reconnect and on the periodic tick (a channel with a
	// non-zero cursor is one a client here cares about).
	mu     sync.Mutex
	lastID map[string]int64
}

func (b *Bus) replayN() int {
	if b.replayLimit > 0 {
		return b.replayLimit
	}
	return DefaultReplayLimit
}

func (b *Bus) rescanInterval() time.Duration {
	if b.rescanEvery > 0 {
		return b.rescanEvery
	}
	return DefaultRescanEvery
}

// knownChannels snapshots every channel this replica has a non-zero
// cursor for — the set re-scanned on reconnect / periodic tick.
func (b *Bus) knownChannels() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.lastID))
	for ch, id := range b.lastID {
		if id > 0 {
			out = append(out, ch)
		}
	}
	return out
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
	rescan := time.NewTicker(b.rescanInterval())
	defer rescan.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-rescan.C:
			// Periodic safety re-scan (Fix #1): defense against a
			// silently-dropped NOTIFY with no overflow/reconnect
			// signal. Cheap when nothing is new (deliverThrough's
			// upTo<=from guard early-returns after a 0-row chunk).
			b.rescanAll(ctx)
		case n, ok := <-notes:
			if !ok {
				return nil
			}
			if n.isReconnect() {
				// Listener reconnected after a DB blip: re-scan every
				// known channel NOW so events that landed during the
				// outage are delivered with bounded latency instead
				// of waiting for the next per-channel NOTIFY (Fix #1).
				b.rescanAll(ctx)
				continue
			}
			b.deliverThrough(ctx, n.Channel, n.ID)
		}
	}
}

// rescanAll re-runs the chunked gap-recovery scan for every channel
// this replica has a non-zero cursor for. Idempotent: deliverThrough's
// per-channel lastID cursor + monotonic guard dedupe, so a re-scan
// that finds nothing new is a no-op and there is no double-delivery or
// reorder. Used by both the reconnect signal and the periodic tick.
func (b *Bus) rescanAll(ctx context.Context) {
	for _, ch := range b.knownChannels() {
		if ctx.Err() != nil {
			return
		}
		// upTo is unbounded here: deliverThrough re-derives the real
		// ceiling from the table per chunk, so "deliver everything
		// past my cursor" is expressed as a max int64.
		b.deliverThrough(ctx, ch, maxInt64)
	}
}

const maxInt64 = int64(^uint64(0) >> 1)

// deliverThrough fans out every not-yet-delivered event on channel up
// to and including upTo. It always re-reads from the table starting
// after the last id this replica delivered, so a NOTIFY that was never
// received still gets caught here (the gap-recovery property).
func (b *Bus) deliverThrough(ctx context.Context, channel string, upTo int64) {
	limit := b.replayN()
	for {
		if ctx.Err() != nil {
			return
		}
		b.mu.Lock()
		from := b.lastID[channel]
		b.mu.Unlock()

		if upTo <= from {
			return // already delivered (duplicate NOTIFY / out-of-order)
		}

		// One bounded chunk: at most `limit` rows past the cursor.
		// Per-iteration memory and Bus.Run block-time are O(limit)
		// regardless of backlog size, so other channels aren't
		// starved after a long outage (Fix #2). b.mu is NOT held
		// across the DB scan (unchanged from the original).
		evs, err := b.backend.Replay(ctx, channel, from, limit)
		if err != nil {
			b.log.Warn("eventbus: replay during fan-out failed; will retry on next notify",
				"err", err, "channel", channel, "from", from,
				"event", "eventbus_replay_failed")
			return
		}

		maxID := from
		stopped := false
		for _, e := range evs {
			if e.ID > upTo {
				// Beyond what this NOTIFY announced; a later NOTIFY
				// (or re-scan) delivers it (keeps ordering tight).
				stopped = true
				break
			}
			b.hub.Publish(e.Channel, e.toHubEvent())
			if e.ID > maxID {
				maxID = e.ID
			}
		}

		// Advance the cursor strictly forward. The monotonic guard
		// keeps re-scan idempotent (no double-delivery / reorder).
		b.mu.Lock()
		if maxID > b.lastID[channel] {
			b.lastID[channel] = maxID
		}
		b.mu.Unlock()

		// Terminate on a short read (whole backlog drained) or once
		// we've passed upTo. A full chunk that made no forward
		// progress (maxID==from: every row was > upTo) also stops —
		// guarantees a strictly increasing cursor / no infinite loop.
		if stopped || len(evs) < limit || maxID == from {
			return
		}
	}
}

// Replay returns AT MOST replayN() events on channel with
// id > lastEventID, for the on-(re)connect catch-up handshake
// (Story 19.2 AC3). The cap (Fix #2) bounds the handshake pull so a
// reconnect after a long outage cannot OOM the API: the client
// already persists each event's "_event_id" and passes the largest
// back as Last-Event-ID, so when the gap exceeds the cap it simply
// re-handshakes and resumes from the last id it received — no
// lost-event window (the cursor strictly advances), no infinite loop
// (each handshake makes >=1 row of progress until a short read).
// lastEventID 0 → from the start of the retained tail.
func (b *Bus) Replay(ctx context.Context, channel string, lastEventID int64) ([]Event, error) {
	return b.backend.Replay(ctx, channel, lastEventID, b.replayN())
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
