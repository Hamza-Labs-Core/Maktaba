# Implementation Plan — Story 7.16 WebSocket Fan-out

> Companion to [story-07-16-websocket-fanout.md](story-07-16-websocket-fanout.md).
> Real-time delivery for jobs, library, and playback channels. SSE
> fallback for proxies that strip Upgrade.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET /ws/jobs`, `GET /ws/library/{id}`, `GET /ws/playback/{video_id}`. |
| WS library | `github.com/coder/websocket` (per architecture §1.2 / §2.1; the package was renamed from `nhooyr.io/websocket`). |
| Channels | Postgres LISTEN with one subscription per channel name per replica; in-process fanout to connected clients. |
| Replay | A new `events` table stores fired events for `events_retention_hours` (default 24 h). Reconnects with `?since=<at>` replay last 1000 events from the table. |
| SSE fallback | Same handler; when `Accept: text/event-stream`, no upgrade is attempted and we stream SSE frames over the same code path. |
| Out of scope | Auth issuance (Epic 10); per-user permission resolution beyond a stub (Epic 10 owns `userFromCtx`). |

## 1. Architecture diagram

```
                 ┌─────────────────────────────┐
                 │ Postgres                    │
                 │  NOTIFY 'jobs.new', etc.    │
                 └──────────┬──────────────────┘
                            │ LISTEN (one conn per channel per replica)
                 ┌──────────▼──────────────────┐
                 │ pubsub.Hub (in-proc)        │
                 │  channel name → []*Client   │
                 │  per-client send queue      │
                 │  backpressure / replay      │
                 └────┬───────────────────┬────┘
                      │                   │ HTTP/SSE write
       ┌──────────────▼──┐         ┌──────▼─────────────┐
       │ WebSocket conn  │         │ SSE conn           │
       │ (client A)      │         │ (client B)         │
       └─────────────────┘         └────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/ws/handler.go` | Three GETs + Accept-Type negotiation. |
| `api/internal/ws/hub.go` | Subscription registry + per-channel fanout. |
| `api/internal/ws/client.go` | Per-connection state: send queue, backpressure, heartbeat. |
| `api/internal/ws/sse.go` | SSE writer impl. |
| `api/internal/ws/listener.go` | Postgres LISTEN reconnect-with-backoff. |
| `api/internal/ws/replay.go` | `?since=<at>` replay from `events`. |
| `api/internal/ws/types.go` | Envelope. |
| `api/internal/ws/handler_test.go` | Integration. |
| `api/internal/ws/hub_test.go` | Unit. |
| `shared/db/queries/events.sql` | sqlc inputs. |
| `shared/db/migrations/0020_events.sql` | Schema. |

## 3. SQL — events table

`shared/db/migrations/0020_events.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE events (
    id          BIGSERIAL PRIMARY KEY,
    channel     TEXT NOT NULL,            -- 'jobs.new', 'jobs.flag_set', ...
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX events_channel_at_idx
    ON events (channel, occurred_at DESC, id DESC);

-- A retention sweep job (out-of-band) deletes rows older than the
-- retention window; this story ships only the schema + index.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS events;
-- +goose StatementEnd
```

Each NOTIFY-emitting trigger in earlier migrations gets a sister
`INSERT INTO events` so reconnects can replay. The triggers are amended
in this story's migration via `CREATE OR REPLACE FUNCTION` for each:

```sql
CREATE OR REPLACE FUNCTION processing_jobs_notify_new() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    payload jsonb := json_build_object(
        'id', NEW.id, 'video_id', NEW.video_id,
        'stage', NEW.stage, 'priority', NEW.priority);
BEGIN
    INSERT INTO events (channel, payload) VALUES ('jobs.new', payload);
    PERFORM pg_notify('jobs.new', payload::text);
    RETURN NEW;
END;
$$;
```

The same shape applies to `jobs.flag_set`, `jobs.progress`, etc. The
in-process fanout publishes for SQLite directly into both the bus and
the events table.

## 4. Type definitions

```go
// api/internal/ws/types.go
package ws

import (
    "encoding/json"
    "time"
)

// Envelope is what the client receives on the wire. Go's encoding/json has
// no `inline` directive, so we marshal in two steps: first the envelope
// header (Type, At), then merge in the payload object's keys at the top
// level. The result on the wire is `{"type":..., "at":..., <payload>}`.
type Envelope struct {
    Type    string          `json:"type"`     // <channel>.<event>
    At      time.Time       `json:"at"`
    Payload json.RawMessage `json:"-"`        // merged at marshal time
}

// MarshalJSON merges Payload's top-level keys into the envelope object.
// If Payload is empty or not a JSON object, only Type/At are emitted.
func (e Envelope) MarshalJSON() ([]byte, error) {
    obj := map[string]any{"type": e.Type, "at": e.At}
    if len(e.Payload) > 0 {
        var p map[string]json.RawMessage
        if err := json.Unmarshal(e.Payload, &p); err == nil {
            for k, v := range p { obj[k] = v }
        }
    }
    return json.Marshal(obj)
}

const (
    CodeSlowConsumer = 1011
    CodeUnauthorized = 4401
    CodeForbidden    = 4403
)
```

## 5. Hub

```go
// api/internal/ws/hub.go
package ws

import (
    "context"
    "sync"
    "time"
)

type Hub struct {
    mu       sync.RWMutex
    subs     map[string]map[*Client]struct{}     // channel → set
    listener Listener                            // wraps pgx LISTEN
}

func NewHub(l Listener) *Hub {
    return &Hub{subs: map[string]map[*Client]struct{}{}, listener: l}
}

// Subscribe attaches a client to a channel; on first subscriber per
// channel the hub starts a Postgres LISTEN for that channel. Unsubscribe
// is symmetric.
func (h *Hub) Subscribe(ctx context.Context, ch string, c *Client) error {
    h.mu.Lock()
    set, ok := h.subs[ch]
    if !ok {
        set = map[*Client]struct{}{}
        h.subs[ch] = set
        if err := h.listener.Listen(ctx, ch, h.dispatch); err != nil {
            delete(h.subs, ch); h.mu.Unlock(); return err
        }
    }
    set[c] = struct{}{}
    h.mu.Unlock()
    return nil
}

func (h *Hub) Unsubscribe(ch string, c *Client) {
    h.mu.Lock()
    if set, ok := h.subs[ch]; ok {
        delete(set, c)
        if len(set) == 0 {
            delete(h.subs, ch)
            h.listener.Unlisten(ch)
        }
    }
    h.mu.Unlock()
}

func (h *Hub) dispatch(ch string, payload []byte) {
    h.mu.RLock()
    set := h.subs[ch]
    h.mu.RUnlock()
    for c := range set {
        if !c.PushFrame(ch, payload) {
            // backpressure violated → close
            c.Close(CodeSlowConsumer, "slow-consumer")
        }
    }
}
```

## 6. Client

```go
// api/internal/ws/client.go
package ws

import (
    "context"
    "encoding/json"
    "time"

    "github.com/coder/websocket"
)

const (
    sendQueueDepth = 1000
    sendQueueBytes = 1 << 20 // 1 MiB
    pingInterval   = 30 * time.Second
    pongDeadline   = 10 * time.Second
)

type Client struct {
    conn   *websocket.Conn
    send   chan []byte
    bytes  atomic.Int64
    user   User
    perms  Permissions
}

// PushFrame is non-blocking; returns false on overflow → caller closes.
func (c *Client) PushFrame(channel string, payload []byte) bool {
    frame, _ := envelope(channel, payload)
    if len(c.send) >= sendQueueDepth {
        return false
    }
    if c.bytes.Load()+int64(len(frame)) > sendQueueBytes {
        return false
    }
    select {
    case c.send <- frame:
        c.bytes.Add(int64(len(frame)))
        return true
    default:
        return false
    }
}

func (c *Client) writeLoop(ctx context.Context) {
    ticker := time.NewTicker(pingInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case f, ok := <-c.send:
            if !ok { return }
            wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
            err := c.conn.Write(wctx, websocket.MessageText, f)
            cancel()
            c.bytes.Add(-int64(len(f)))
            if err != nil { return }
        case <-ticker.C:
            pctx, cancel := context.WithTimeout(ctx, pongDeadline)
            err := c.conn.Ping(pctx)
            cancel()
            if err != nil { return }
        }
    }
}
```

## 7. Handler

```go
// api/internal/ws/handler.go
package ws

import (
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/coder/websocket"
)

func (h *handler) jobs(w http.ResponseWriter, r *http.Request) {
    user, perr := authenticate(r)
    if perr != nil {
        if isWS(r) {
            // For WS, RFC says we still complete the upgrade then close
            // with a 4xxx code. For pre-upgrade rejection (no upgrade
            // header), we send 401.
            w.WriteHeader(http.StatusUnauthorized); return
        }
        httperror.Write(w, r, perr); return
    }

    if r.Header.Get("Accept") == "text/event-stream" {
        h.handleSSE(w, r, []string{"jobs.new","jobs.flag_set","jobs.progress","jobs.heartbeat","jobs.reaped"}, user)
        return
    }

    conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
        InsecureSkipVerify: false, // TLS / origin check via middleware
    })
    if err != nil { return }

    cl := newClient(conn, user, h.perms.For(user))
    if err := h.hub.Subscribe(r.Context(), "jobs.new", cl); err != nil {
        cl.Close(1011, "subscribe-failed"); return
    }
    // ... subscribe to the other jobs.* channels.

    if since := r.URL.Query().Get("since"); since != "" {
        if err := h.replay(cl, "jobs.*", since); err != nil { /* log */ }
    }

    cl.Run(r.Context())
}
```

The `library` and `playback` handlers follow the same shape but pull
`{id}` from the path and verify the user can read that resource via
`h.perms.CanRead(...)`.

## 8. Listener (LISTEN with backoff)

```go
// api/internal/ws/listener.go
package ws

import (
    "context"
    "math/rand"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
)

type pgListener struct {
    pool *pgxpool.Pool
    quit chan struct{}
}

func (p *pgListener) Listen(ctx context.Context, channel string, on func(string, []byte)) error {
    go p.runForever(ctx, channel, on)
    return nil
}

func (p *pgListener) runForever(ctx context.Context, channel string, on func(string, []byte)) {
    backoff := 100 * time.Millisecond
    for {
        if ctx.Err() != nil { return }
        if err := p.session(ctx, channel, on); err != nil {
            // jittered exponential backoff up to 30 s.
            t := time.Duration(rand.Int63n(int64(backoff)))
            time.Sleep(backoff + t)
            backoff *= 2
            if backoff > 30*time.Second { backoff = 30 * time.Second }
            continue
        }
        backoff = 100 * time.Millisecond
    }
}

func (p *pgListener) session(ctx context.Context, channel string, on func(string, []byte)) error {
    conn, err := p.pool.Acquire(ctx)
    if err != nil { return err }
    defer conn.Release()
    if _, err := conn.Exec(ctx, "LISTEN "+pgQuoteIdent(channel)); err != nil { return err }
    for {
        n, err := conn.Conn().WaitForNotification(ctx)
        if err != nil { return err }
        on(n.Channel, []byte(n.Payload))
    }
}
```

## 9. Replay

```go
// api/internal/ws/replay.go
const replayCap = 1000

func (h *handler) replay(c *Client, channelGlob, sinceISO string) error {
    since, err := time.Parse(time.RFC3339, sinceISO)
    if err != nil { return err }
    rows, err := h.db.EventsSince(c.ctx, EventsSinceParams{
        ChannelGlob: channelGlob, Since: since, Limit: replayCap,
    })
    if err != nil { return err }
    for _, r := range rows {
        c.PushFrame(r.Channel, r.Payload)
    }
    if len(rows) >= replayCap {
        // gap notice
        c.PushFrame("system.gap", marshal(map[string]any{
            "from": rows[len(rows)-1].At, "to": rows[0].At,
            "reason": "replay-cap",
        }))
    }
    return nil
}
```

## 10. SQL — events queries

`shared/db/queries/events.sql`:

```sql
-- name: EventsSince :many
SELECT channel, payload, occurred_at
  FROM events
 WHERE channel LIKE $1     -- e.g. 'jobs.%' for the jobs WS
   AND occurred_at >= $2
 ORDER BY occurred_at DESC, id DESC
 LIMIT $3;

-- name: SweepEvents :exec
DELETE FROM events
 WHERE occurred_at < now() - ($1 * interval '1 hour');
```

## 11. Test plan

### 11.1 Unit (`hub_test.go`)

| Test | What it pins |
|---|---|
| `TestSubscribeStartsListenOnce` | First sub starts `LISTEN`; second on same channel does not. |
| `TestUnsubscribeStopsListenWhenLast` | Last sub leaving stops `LISTEN`. |
| `TestDispatchSlowConsumerClosed` | Mock client whose send channel is full → Hub calls `Close(1011)`. |

### 11.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestNoAuthClosed4401` | WS upgrade without auth → close 4401. |
| `TestPingPong` | Connect, idle 60 s → at least 2 ping/pong cycles. |
| `TestJobStateChangePropagates` | Insert a `processing_jobs` row → connected client sees `jobs.new` envelope within 200 ms. |
| `TestSlowConsumerClose` | Stop reading on the client → server closes 1011 within 5 s. |
| `TestSSEFallback` | GET `/ws/jobs` with `Accept: text/event-stream` → first 10 events match the WS variant. |
| `TestForbiddenLibraryDuringConnect` | Connect to `/ws/library/X` for a library the user cannot read → close 4403. |
| `TestReconnectReplay` | Disconnect, insert 5 events, reconnect with `?since=<at>` → those 5 events delivered. |
| `TestReplayCap1000` | Insert 1500 events → reconnect replays last 1000 + a `system.gap` event. |
| `TestPostgresDropReconnects` | Kill the LISTEN connection (pool eviction) → reconnect within 1 s; no duplicate or lost notifications during steady-state (proven via in-band counter). |
| `TestThousandConcurrentConnections` | Load test with 1000 idle WS conns → memory under 200 MB. |

## 12. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Upgrade behind a proxy stripping `Connection: Upgrade` | The handler returns 426 by default; client falls back to SSE via `Accept: text/event-stream`. | `TestSSEFallback` |
| Two API replicas LISTEN the same channel | Both receive NOTIFY; each delivers to its connected clients. Document this in operations. | Documented |
| Client subscribed to library X loses access mid-session | Next event is intercepted by the perms check; connection closed with 4403. | `TestForbiddenLibraryDuringConnect` (variant for runtime) |
| Postgres restarts | Listener reconnect with backoff; replay window covers the gap. | `TestPostgresDropReconnects` |
| Slow consumer | 1 MiB / 1000 frame cap; close 1011. | `TestSlowConsumerClose` |
| Replay cap exceeded | Last 1000 events + a `system.gap` notice. | `TestReplayCap1000` |
| `?since` malformed | 400 `invalid-query-parameter`. | Unit |
| Channel name not in the canonical list (`jobs.new`, `jobs.flag_set`, …) | Server only `LISTEN`s the documented set; unknown channels are ignored. | Documented |
| Heartbeat ping fails (network glitch) | Connection closed; client reconnects with `?since` to replay missed events. | `TestPingPong` |

## 13. Acceptance checklist

- [ ] WS auth at handshake; 4401 on failure.
- [ ] Per-resource subscription scoping enforced.
- [ ] Envelope `{type, at, ...payload}` shape stable.
- [ ] Slow-consumer close 1011; replay via `?since` works up to 1000 events.
- [ ] Heartbeats: 30 s ping, 10 s pong window.
- [ ] SSE fallback delivers identical events.
- [ ] Canonical channel names (`jobs.new`, `jobs.flag_set`, `jobs.progress`, `jobs.heartbeat`, `jobs.reaped`, `jobs.force_pause`, `playback.progress`, `playback.completed`, `library.video_added`, `library.video_updated`).
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.16.
