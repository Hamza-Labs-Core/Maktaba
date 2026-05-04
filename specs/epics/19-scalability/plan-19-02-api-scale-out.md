# Implementation Plan — Story 19.2 API Horizontal Scale-Out

> Companion to [story-19-02-api-scale-out.md](story-19-02-api-scale-out.md).
> Two stateless API replicas behind any L7 LB; WS events fan out via Postgres
> NOTIFY+`events` table; rolling restart preserves clients on the other replica.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Statelessness | API holds no in-memory session state; JWTs verified per request via JWKS cache. |
| Event bus | Postgres LISTEN/NOTIFY for the fast path; `events` table for durable replay. |
| Client recovery | `last_event_id` cursor; replicas read from `events` to fill gaps. |
| Rolling restart | Drain mode signals LB via `/healthz` returning 503 while completing in-flight. |
| Out of scope | LB choice (caddy / nginx / cloud) — works with any L7 with WS support. |

## 1. Project layout

```
api/internal/
├── eventbus/
│   ├── bus.go               # publish + subscribe interface
│   ├── postgres.go          # LISTEN/NOTIFY backend
│   ├── replay.go            # events-table replay
│   ├── ringbuf.go           # same-replica fast-path (Story 7.16)
│   ├── pruner.go            # 7-day retention
│   ├── overflow.go          # NOTIFY queue overflow → poll mode
│   └── bus_test.go
├── ws/
│   ├── hub.go
│   ├── client.go
│   └── handshake.go         # last_event_id cursor
├── healthz/
│   ├── handler.go           # 200/503 + drain mode
│   └── handler_test.go
└── ...

shared/db/migrations/
└── 00xx_events_table.sql
```

## 2. Events table schema

> **Plan-introduced extension to story AC3.** Story 19.2 AC3 specifies
> only `(id, channel, payload, created_at)` for the durable replay row. This
> plan adds nullable `user_id`/`library_id` columns to enable per-user replay
> filtering (`§5` `events_user_id_id_idx`) and library-scoped fan-out without
> a JSON probe. These columns are an explicit override of AC3 and require
> the story to be amended; tracking under cross-cutting §1.1 of
> `PLAN_REVIEW_18_24.md`.

```sql
-- 00xx_events_table.sql
CREATE TABLE events (
    id          BIGSERIAL PRIMARY KEY,
    channel     TEXT NOT NULL,
    payload     JSONB NOT NULL,
    user_id     UUID,                    -- plan-introduced extension to AC3
    library_id  UUID,                    -- plan-introduced extension to AC3
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_channel_id_idx     ON events (channel, id DESC);
CREATE INDEX events_created_at_idx     ON events (created_at);
CREATE INDEX events_user_id_id_idx     ON events (user_id, id DESC) WHERE user_id IS NOT NULL;
```

`BIGSERIAL` ensures monotonic `id` survives pruning (sequence is never reset).

## 3. Publish path

> **Design override (vs Story AC3).** Story AC3 implies small payloads
> *could* ride NOTIFY alone (no row in `events`). This plan persists a row
> AND emits NOTIFY for every publish, regardless of payload size. Rationale:
> uniform `last_event_id` replay semantics (TC3, TC4) and a single retention
> story (`pruner`). The cost is one extra row per event — negligible at the
> capacity floor (Story 19.1) and bounded by the 7-day retention. NOTIFY-only
> events would require a separate "transient" channel and complicate replay;
> we accept the row-write tax in exchange for a simpler durable contract.
> The `ref:true` marker (see below) signals to listeners that the
> NOTIFY payload was truncated and the row must be fetched by id.

```go
// eventbus/postgres.go
func (b *PGBus) Publish(ctx context.Context, ev Event) error {
    var id int64
    err := b.db.QueryRowContext(ctx, `
        INSERT INTO events (channel, payload, user_id, library_id)
        VALUES ($1, $2, $3, $4) RETURNING id
    `, ev.Channel, ev.Payload, ev.UserID, ev.LibraryID).Scan(&id)
    if err != nil { return err }

    // Fast-path NOTIFY: embed payload inline when it fits within the 8 KiB
    // bound. Listeners with `ref:false` can dispatch directly without a
    // round-trip to `events`. Large payloads set `ref:true` and listeners
    // must fetch the row by id.
    inline, _ := json.Marshal(map[string]any{"id": id, "ch": ev.Channel, "data": ev.Payload})
    var payload []byte
    if len(inline) <= 8192 {
        payload = inline
    } else {
        // AC3: NOTIFY payload bound at 8 KiB → notify with id only
        payload, _ = json.Marshal(map[string]any{"id": id, "ch": ev.Channel, "ref": true})
    }
    if _, err := b.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, "maktaba_events", string(payload)); err != nil {
        return err
    }
    b.ring.Append(id, ev)        // same-replica fast path
    return nil
}
```

## 4. Subscribe path

```go
// eventbus/postgres.go
func (b *PGBus) Listen(ctx context.Context, ch chan<- Event) error {
    pl, err := pq.NewListener(b.dsn, time.Second, time.Minute, b.notifyEvent)
    if err != nil { return err }
    if err := pl.Listen("maktaba_events"); err != nil { return err }
    go func() {
        for {
            select {
            case <-ctx.Done(): return
            case n := <-pl.Notify:
                if n == nil { continue }                 // overflow signal
                var hdr struct{
                    ID   int64           `json:"id"`
                    Ch   string          `json:"ch"`
                    Ref  bool            `json:"ref"`
                    Data json.RawMessage `json:"data"`
                }
                _ = json.Unmarshal([]byte(n.Extra), &hdr)
                // Fast-path: when ref==false the publisher inlined the payload
                // and we can dispatch without a round-trip. Only fetch the row
                // by id when ref==true (truncated payload).
                if hdr.Ref {
                    ev, err := b.fetchByID(ctx, hdr.ID)
                    if err == nil { ch <- ev }
                } else {
                    ch <- Event{ID: hdr.ID, Channel: hdr.Ch, Payload: hdr.Data}
                }
            }
        }
    }()
    return nil
}
```

## 5. WS handshake with `last_event_id`

```go
// ws/handshake.go
func (h *Handler) Upgrade(w http.ResponseWriter, r *http.Request) {
    last, _ := strconv.ParseInt(r.URL.Query().Get("last_event_id"), 10, 64)
    conn, err := upgrader.Upgrade(w, r, nil); if err != nil { return }
    sub := h.hub.Subscribe(r.Context(), userFrom(r))
    if last > 0 {
        if err := h.replay.Stream(r.Context(), sub.UserID, last, conn); err != nil {
            slog.Warn("ws replay failed", "err", err)
        }
    }
    sub.Pump(conn)
}
```

```go
// eventbus/replay.go
func (r *Replay) Stream(ctx context.Context, userID uuid.UUID, after int64, w MessageWriter) error {
    rows, err := r.db.QueryContext(ctx, `
        SELECT id, channel, payload FROM events
         WHERE id > $1 AND (user_id IS NULL OR user_id = $2)
         ORDER BY id ASC
         LIMIT 1000
    `, after, userID)
    if err != nil { return err }
    defer rows.Close()
    for rows.Next() {
        var id int64; var ch string; var pl []byte
        if err := rows.Scan(&id, &ch, &pl); err != nil { return err }
        if err := w.WriteJSON(map[string]any{"id": id, "ch": ch, "data": json.RawMessage(pl)}); err != nil { return err }
    }
    return nil
}
```

## 6. NOTIFY overflow fallback (EC1)

```go
// eventbus/overflow.go
type OverflowDetector struct {
    drops       atomic.Int64
    window      time.Duration            // overflow_window_sec (default 60 s)
    threshold   int64                    // overflow_threshold (default 100)
    pollMode    atomic.Bool
    pollTrigger func()
}

func (l *PGBus) notifyEvent(ev pq.ListenerEventType, err error) {
    if ev == pq.ListenerEventConnected || ev == pq.ListenerEventReconnected {
        l.overflow.pollMode.Store(false)
        return
    }
    if ev == pq.ListenerEventDisconnected || strings.Contains(err.Error(), "queue overflow") {
        l.overflow.drops.Add(1)
        if l.overflow.drops.Load() >= l.overflow.threshold {
            l.overflow.pollMode.Store(true)
            l.overflow.pollTrigger()
        }
    }
}

// poll mode: every 250 ms, SELECT events WHERE id > last_seen
```

## 7. Pruner

```go
// eventbus/pruner.go
func (p *Pruner) Run(ctx context.Context) {
    t := time.NewTicker(time.Hour)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            cutoff := time.Now().Add(-p.retention)
            res, err := p.db.ExecContext(ctx, `DELETE FROM events WHERE created_at < $1`, cutoff)
            if err == nil {
                n, _ := res.RowsAffected()
                metrics.EventsPruned.Add(float64(n))
            }
        }
    }
}
```

`BIGSERIAL` is intentionally never reset; `last_event_id` advances monotonically across prune (TC4).

## 8. Rolling restart drain

```go
// healthz/handler.go
type Health struct{ drain atomic.Bool; inflight atomic.Int64 }

func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if h.drain.Load() && h.inflight.Load() == 0 {
        w.WriteHeader(503); return
    }
    if h.drain.Load() {
        w.WriteHeader(503); return     // signal LB to remove
    }
    w.WriteHeader(200)
}

func (h *Health) StartDrain() { h.drain.Store(true) }
```

SIGTERM handler:

```go
go func() {
    <-sigchan
    health.StartDrain()
    time.Sleep(driainGracePeriod)         // LB removes
    server.Shutdown(ctx)                  // stops new conns; in-flight finishes
}()
```

WS clients on the draining replica receive a `close: 1012 service_restart`; on reconnect to the surviving replica they replay via `last_event_id`.

## 9. JWKS cross-replica (EC3)

JWKS cache is per-replica with TTL=5 min (Story 18.8). Replica A rotates → replica B picks up within ≤ 5 min.

## 10. Test cases

### TC1 — Round-robin
Stand up two replicas; LB round-robins 1,000 requests with same Bearer JWT. All return 200, identical payloads, same `etag` and same `version_id`.

### TC2 — WS fan-out
100 clients, half on each replica. Trigger 1,000 events sequentially; assert each client receives all 1,000 in monotonic order. Across-replica p95 ≤ 250 ms verified by timestamps captured server-side at NOTIFY emit and client-side on receive.

### TC3 — Rolling restart
50 clients on replica A. SIGTERM A. Clients receive `close 1012`, reconnect with `last_event_id`. Assert: every event triggered during the 5-second outage replays from `events` table; no duplicate or skipped IDs.

### TC4 — Retention
Backfill 7-day-old rows. Run pruner. Assert: rows older than 7d gone, `MAX(id)` continues from the surviving sequence value (no rollback). New publishes get `id > previous max`.

### TC5 — EC1 NOTIFY-overflow detection latency
Force the listener into a `pq.ListenerEventDisconnected` / "queue overflow" state by suspending the listener goroutine while publishing past the
`overflow_threshold` within `overflow_window_sec`. Capture the timestamp of
the first drop and the timestamp at which `pollMode.Load()` flips to `true`.
Assert: `pollModeFlipAt - firstDropAt ≤ 5 s` (story EC1) and that subsequent
events are observed by all subscribers within an additional
`poll_fallback_interval_ms` window. Reconnect and assert `pollMode` clears.

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 NOTIFY overflow | story | OverflowDetector → poll mode at threshold (configurable). |
| EC2 clock skew | story | All `now()` reads from Postgres `now()`. |
| EC3 JWKS rotation | story | Per-replica TTL cache (5 min). |
| Replica fails publish-after-NOTIFY (split pub) | impl | Subscribers always re-fetch by id; truncated payload fine. |
| Same client connects to both replicas | impl | Each connection has its own subscription; client-side dedupe via `id`. |

## 12. Configuration

```yaml
api:
  events:
    notify_payload_max_bytes: 8192
    # OverflowDetector: trip into poll mode when more than `overflow_threshold`
    # drops occur within a sliding window of `overflow_window_sec` seconds.
    # (Replaces the old conflated `overflow_threshold_per_minute` knob.)
    overflow_threshold: 100
    overflow_window_sec: 60
    poll_fallback_interval_ms: 250
    retention_days: 7
    prune_interval: 1h
  drain_grace: 5s
```

## 13. Dependencies

- Epic 7 API server (Story 7.16 in-mem ring).
- Epic 18 cache layout (JWKS).
- Epic 21 (metrics: `events_published_total`, `events_pruned_total`, `notify_overflow_total`).
- Epic 22 devops (LB config examples).
