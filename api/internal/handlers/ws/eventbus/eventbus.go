// Package eventbus is the cross-replica WebSocket event bus
// (Epic 19 Story 19.2, HLB-353).
//
// Why this exists
// ----------------
// The ws.Hub is in-memory single-process: an event published while a
// WS client's socket lives on a *different* API replica never reaches
// that client, and a client that reconnects loses everything it
// missed. This package closes that gap with the exact pattern the
// codebase already uses for cross-process fan-out — a durable Postgres
// table plus LISTEN/NOTIFY (slot-0002 `jobs.new`, slot-0005
// `videos.new`) — rather than inventing a new bus abstraction.
//
// Shape
// -----
//   - Every event is appended to the durable `events` table (migration
//     slot 0061). The row's BIGSERIAL `id` is a process-global
//     monotonic cursor.
//   - The slot-0061 AFTER INSERT trigger fires
//     `pg_notify('ws.events', {id,channel})` — a bounded frame (id +
//     channel only, far under the 8 KiB NOTIFY limit). The full
//     payload is never in the NOTIFY; it is read back from the table.
//   - Every replica runs one LISTEN loop (Bus.Run). On each
//     notification it reads the row by id and fans it out to its
//     *local* ws.Hub. Because the table is the source of truth and the
//     loop catches up from the last id it delivered, a replica that
//     missed a NOTIFY (overflow, restart) still delivers every event
//     via the replay-on-gap scan.
//   - On WS (re)connect a client passes its `last_event_id`; Bus.Replay
//     returns every event on its channel with a larger id so no event
//     is lost across a reconnect (Story 19.2 AC3).
//
// The Backend seam lets the unit tier exercise the full
// cross-replica + replay contract against an in-memory backend that
// models the table + NOTIFY semantics exactly (one backend shared by
// two Bus instances == two replicas over one Postgres). The
// integration tier (build tag `integration`) runs the same flow
// against a real Postgres + the slot-0061 trigger.
package eventbus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// Event is one bus message. Channel is the ws.Hub fan-out key (the
// exact strings ws.go routes on: "jobs", "library:<id>",
// "playback:<video_id>"). ID/At are assigned by the backend on Append
// and are zero on the publish path.
type Event struct {
	ID      int64          `json:"id"`
	Channel string         `json:"channel"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
	At      time.Time      `json:"at"`
}

// toHubEvent maps a bus Event onto the ws.Hub envelope. The bus id is
// threaded into the payload as "_event_id" so a WS client can persist
// it and pass it back as last_event_id on reconnect (Story 19.2 AC3
// cursor handshake) without changing the ws.Event wire shape.
func (e Event) toHubEvent() ws.Event {
	payload := map[string]any{}
	for k, v := range e.Payload {
		payload[k] = v
	}
	payload["_event_id"] = e.ID
	at := e.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return ws.Event{Type: e.Type, At: at, Payload: payload}
}

// Notification is the bounded frame carried by pg_notify on the
// 'ws.events' channel — id + channel only. It mirrors the JSON the
// slot-0061 events_notify() trigger builds.
type Notification struct {
	ID      int64  `json:"id"`
	Channel string `json:"channel"`
}

// ReconnectSignal is the sentinel a Backend pushes onto its Listen
// stream when the underlying listener reconnects after a DB blip
// (pq.Listener delivers a nil notification on reconnect; MemoryBackend
// pushes it via SignalReconnect). It is distinguishable from every
// real frame because the slot-0061 trigger never emits id 0 / empty
// channel (BIGSERIAL starts at 1, channel is NOT NULL). Surfacing it
// through the existing Listen seam — instead of a new Backend method —
// keeps the interface (and the integration tier) source-compatible.
var ReconnectSignal = Notification{ID: 0, Channel: ""}

func (n Notification) isReconnect() bool {
	return n.ID == 0 && n.Channel == ""
}

// DefaultReplayLimit bounds a single Replay/deliverThrough chunk so a
// long-outage backlog is drained in fixed-size pages rather than
// materialized whole (OOM / Bus.Run stall). Overridable per-Bus for
// tests (Bus.replayLimit) and forms the handshake cap (ws.go) so the
// client re-handshakes for the remainder.
const DefaultReplayLimit = 500

// Backend is the durable + notify substrate. PostgresBackend is the
// production implementation (events table + pq.Listener on
// 'ws.events'); MemoryBackend models the identical contract for the
// unit tier. A single Backend instance shared by two Bus instances is
// exactly "two API replicas over one Postgres".
type Backend interface {
	// Append durably stores ev and returns its assigned monotonic id.
	// Implementations MUST emit a Notification for it (the slot-0061
	// trigger does this in Postgres; MemoryBackend does it inline).
	Append(ctx context.Context, ev Event) (int64, error)

	// Replay returns up to limit stored events on channel with
	// id > afterID, ascending. The bound keeps a long-outage backlog
	// from being materialized whole into memory and blocking Bus.Run;
	// callers chunk by advancing afterID past the last returned id and
	// repeating until a short read (< limit rows). limit <= 0 means
	// the backend default (DefaultReplayLimit).
	Replay(ctx context.Context, channel string, afterID int64, limit int) ([]Event, error)

	// Listen returns the stream of NOTIFY frames for this replica. The
	// channel is closed when ctx is done or the backend is closed. A
	// frame equal to ReconnectSignal (id 0, empty channel) is the
	// listener-reconnect tick: it is not a real event, it tells
	// Bus.Run to re-scan every known channel so events missed during a
	// DB blip are recovered with bounded latency (not "next NOTIFY").
	Listen(ctx context.Context) (<-chan Notification, error)

	// Get reads a single stored event by id (the LISTEN loop reads the
	// full payload back after a bounded NOTIFY). ok is false if the row
	// is absent (pruned).
	Get(ctx context.Context, id int64) (Event, bool, error)

	// Prune deletes events older than the cutoff and returns the count
	// removed (Story 19.2 AC3 7-day retention).
	Prune(ctx context.Context, olderThan time.Time) (int, error)

	// Close releases backend resources (listener connections etc).
	Close() error
}

// encodePayload serialises a payload map to the JSONB column shape.
// nil → "{}" so the column is never NULL (matches the migration
// DEFAULT).
func encodePayload(p map[string]any) ([]byte, error) {
	if len(p) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(p)
}

// decodePayload is the inverse; a malformed/empty value yields a
// non-nil empty map so fan-out never panics.
func decodePayload(b []byte) map[string]any {
	out := map[string]any{}
	if len(b) == 0 {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}
