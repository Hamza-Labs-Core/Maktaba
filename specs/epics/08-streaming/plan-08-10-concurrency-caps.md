# Implementation Plan — Story 8.10 Concurrency Caps and Backpressure

> Companion to [story-08-10-concurrency-caps.md](story-08-10-concurrency-caps.md).
> The story states *what* and *why*; this plan states *how*. Built atop
> [Story 8.5](plan-08-05-hls-transcode.md) (transcode lifecycle),
> [Story 8.7](plan-08-07-hwaccel-detect.md) (NVENC concurrency cap),
> [Story 8.8](plan-08-08-grpc-server.md) (OpenSession queue surface),
> [Story 8.9](plan-08-09-session-store.md) (sessions table state column).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/slot` — atomic counter + queue. No DB writes for the active count (in-memory); queue is mirrored to `streaming_sessions.state='queued'` for visibility / restart. |
| Effective cap | Default `runtime.NumCPU() / 4` (architecture §10.4) unless `[transcode] max_concurrent` overrides. Refresh on `LISTEN streaming_settings_changed`. |
| Hwaccel sub-cap | When `hwaccel.Capabilities.SessionConcurrencyCap > 0`, sessions opened over that sub-cap fall through to libx264 software (Story 8.7's restriction), but still consume one slot from the pool. |
| Queue policy | FIFO across users; per-user fairness is out of scope for v1. Promotion happens on slot release. |
| Queue notification | API-side WebSocket fanout — Streaming exposes a server-streaming gRPC `WatchQueue` (added in this story to extend Story 8.8). The API subscribes and pushes to the client. |
| Queue cleaner | Every 30 s, prune `state='queued'` rows whose owning client has been silent for > 5 min (no heartbeat, no manifest fetch). |
| Out of scope | Per-tenant quotas (different epic). Distributed slot accounting (every host owns its own pool). |

## 1. Architecture diagram

```
        OpenSession (Story 8.8) → manager.Open
                          │
                          ▼
        ┌──────────────────────────────────────────────────────┐
        │  slot.Pool                                           │
        │    total = effectiveMax (cores/4 or override)         │
        │    used  = atomic.Int32                              │
        │    queue = container/list (FIFO)                     │
        │                                                      │
        │  Acquire(ctx, req):                                  │
        │    if used < total:                                  │
        │      used++; return Slot                             │
        │    else if req.AcceptQueue:                          │
        │      append to queue with promote-channel             │
        │      return ErrQueued + QueueState{position, eta}     │
        │    else if req.CanDegradeToDirect720:                 │
        │      return ErrDegradeToDirect (caller flips mode)    │
        │    else:                                             │
        │      return ErrFull                                  │
        │                                                      │
        │  Release(slot):                                      │
        │    used--                                            │
        │    if queue not empty: send on next promote channel   │
        │                                                      │
        │  WatchQueue(sid) chan QueueUpdate                     │
        └──────────────────────────────────────────────────────┘
                          │ promote
                          ▼
        ┌──────────────────────────────────────────────────────┐
        │  manager.promote(sid)                                │
        │    1. SELECT row, switch state='queued' → 'active'   │
        │    2. spawn HLS/DASH session                         │
        │    3. mint a fresh manifest URL                      │
        │    4. push QueueUpdate{state='active', manifest_url}  │
        └──────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/slot/pool.go` | `Pool`, `Acquire`, `Release`, `Snapshot`, `WatchQueue`. |
| `streaming/internal/slot/pool_test.go` | Unit tests with synthetic clock. |
| `streaming/internal/slot/queue.go` | `queueEntry` + helpers (FIFO list under mutex). |
| `streaming/internal/slot/eta.go` | `estimateETA(currentEncodeRT) time.Duration` — uses recent transcode realtime factor from metrics. |
| `streaming/internal/slot/cleaner.go` | `CleanerWorker` — prunes abandoned queued rows. |
| `streaming/internal/slot/cleaner_test.go` | Cleaner end-to-end. |
| `streaming/internal/grpcserver/watch_queue.go` | gRPC server-streaming handler `WatchQueue`. |
| `shared/proto/streaming/v1/streaming.proto` (modified) | Adds `WatchQueue` server-streaming RPC. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/grpcserver/open_session.go` | Wire `accept_queue` through to `slot.Pool`; populate `OpenSessionResponse.queue` when queued; populate `mode='direct-degraded'` when degrading. |
| `streaming/internal/session/manager.go` | Implement `recordQueued` and `promote` paths. |
| `streaming/internal/sessionstore/store.go` | Add `PromoteQueued(sid)` and `MarkQueued(sid)` queries. |
| `streaming/internal/observability/metrics.go` | `slot_pool_total`, `slot_pool_used`, `slot_pool_queued`, `queued_sessions_promoted_total`, `queued_sessions_abandoned_total`. |
| `streaming/configs/streaming.toml.example` | `[transcode] max_concurrent = 0  # 0 → cores/4 auto`, `[queue] cleaner_interval_sec = 30`, `[queue] abandon_after_sec = 300`. |
| `specs/epics/08-streaming/README.md` | Tick 8.10. |

### 2.3 Type definitions

```go
// streaming/internal/slot/pool.go
package slot

import (
    "context"
    "container/list"
    "errors"
    "sync"
    "time"

    "github.com/google/uuid"
)

var (
    ErrFull              = errors.New("slot.pool: full")
    ErrQueued            = errors.New("slot.pool: queued")
    ErrDegradeToDirect   = errors.New("slot.pool: degrade to direct")
    ErrUnknownSession    = errors.New("slot.pool: unknown session")
)

// Pool is the per-host slot manager.
type Pool struct {
    cap        int
    cancelHook func(time.Time)

    mu       sync.Mutex
    used     int
    queue    *list.List   // *queueEntry
    waiters  map[uuid.UUID]*list.Element

    rtfMA    movingAvg    // exponential moving average of recent transcode RTF
    metrics  *Metrics
}

// Slot is the handle returned to a caller. Release() must be called
// exactly once when the session ends.
type Slot struct {
    p        *Pool
    released bool
}

func (s *Slot) Release() {
    if s == nil || s.released {
        return
    }
    s.released = true
    s.p.release()
}

// Request is what callers pass to Acquire.
type Request struct {
    SessionID         uuid.UUID
    UserID            uuid.UUID
    AcceptQueue       bool
    CanDegradeToDirect bool
    SourceDuration    time.Duration   // for ETA calc

    // Promotion channel — Pool sends QueueUpdate here when the session
    // moves from queued → active (one buffered slot; sender drops if full).
    Promote chan QueueUpdate
}

type QueueUpdate struct {
    Position int
    ETASec   int
    State    string  // 'queued' | 'active' | 'abandoned'
}
```

### 2.4 Acquire / Release

```go
// streaming/internal/slot/pool.go (continued)

func New(cap int) *Pool {
    if cap < 1 {
        cap = 1
    }
    return &Pool{
        cap:     cap,
        queue:   list.New(),
        waiters: map[uuid.UUID]*list.Element{},
    }
}

// SetCap is called when settings reload. Lowering does not cancel
// existing sessions (story-stated invariant: "existing sessions are
// *not* killed; new ones respect the new cap").
func (p *Pool) SetCap(n int) {
    p.mu.Lock()
    p.cap = n
    p.mu.Unlock()
}

func (p *Pool) Acquire(ctx context.Context, r Request) (*Slot, error) {
    p.mu.Lock()
    if p.used < p.cap {
        p.used++
        p.metrics.Used.Set(float64(p.used))
        p.mu.Unlock()
        return &Slot{p: p}, nil
    }

    // Full.
    if !r.AcceptQueue && r.CanDegradeToDirect {
        p.mu.Unlock()
        return nil, ErrDegradeToDirect
    }
    if !r.AcceptQueue {
        p.mu.Unlock()
        return nil, ErrFull
    }

    entry := &queueEntry{req: r, enqueuedAt: time.Now()}
    el := p.queue.PushBack(entry)
    p.waiters[r.SessionID] = el
    pos := p.queue.Len()
    eta := p.estimateETA(r.SourceDuration, pos)
    p.metrics.Queued.Set(float64(p.queue.Len()))
    p.mu.Unlock()

    // Send the initial QueueUpdate (best-effort, drop if listener is slow).
    select {
    case r.Promote <- QueueUpdate{Position: pos, ETASec: int(eta.Seconds()), State: "queued"}:
    default:
    }
    return nil, ErrQueued
}

func (p *Pool) release() {
    p.mu.Lock()
    p.used--
    p.metrics.Used.Set(float64(p.used))
    p.promoteHeadLocked()
    p.mu.Unlock()
}

func (p *Pool) promoteHeadLocked() {
    if p.queue.Len() == 0 || p.used >= p.cap {
        return
    }
    head := p.queue.Front()
    p.queue.Remove(head)
    delete(p.waiters, head.Value.(*queueEntry).req.SessionID)
    p.used++
    p.metrics.Queued.Set(float64(p.queue.Len()))
    p.metrics.Used.Set(float64(p.used))
    p.metrics.Promoted.Inc()

    entry := head.Value.(*queueEntry)
    select {
    case entry.req.Promote <- QueueUpdate{Position: 0, ETASec: 0, State: "active"}:
    default:
    }
}

// CancelQueued is called by the cleaner / API when a queued session
// is abandoned.
func (p *Pool) CancelQueued(sid uuid.UUID) {
    p.mu.Lock()
    el, ok := p.waiters[sid]
    if !ok {
        p.mu.Unlock()
        return
    }
    p.queue.Remove(el)
    delete(p.waiters, sid)
    p.metrics.Queued.Set(float64(p.queue.Len()))
    p.metrics.Abandoned.Inc()
    p.mu.Unlock()

    entry := el.Value.(*queueEntry)
    select {
    case entry.req.Promote <- QueueUpdate{Position: 0, ETASec: 0, State: "abandoned"}:
    default:
    }
    close(entry.req.Promote)
}
```

### 2.5 ETA estimation

```go
// streaming/internal/slot/eta.go
package slot

// estimateETA(sourceDuration, position) returns a ballpark wait time:
//   - Each ahead-of-me session has roughly one "transcode burst" of
//     ~10 s of buffer to drain before the player would call CloseSession.
//   - Realistic wait dominated by player buffer drain, not encode time.
//
// We use a 30-event EMA of recent-completed transcode wall-clock to
// estimate. With no history yet, we default to 60 s × position.
func (p *Pool) estimateETA(_ time.Duration, position int) time.Duration {
    if p.rtfMA.Empty() {
        return time.Duration(position) * 60 * time.Second
    }
    return time.Duration(position) * p.rtfMA.AvgSession()
}
```

### 2.6 Cleaner worker

```go
// streaming/internal/slot/cleaner.go
package slot

type CleanerWorker struct {
    Store         *sessionstore.Store
    Pool          *Pool
    Tick          time.Duration
    AbandonAfter  time.Duration
    Now           func() time.Time
}

// Run prunes queued rows whose owning client has gone silent.
// "Silent" = no manifest fetch and no client-side heartbeat for
// AbandonAfter (default 5 min).
func (w *CleanerWorker) Run(ctx context.Context) error {
    if w.Now == nil { w.Now = time.Now }
    t := time.NewTicker(w.Tick)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-t.C:
            _ = w.tick(ctx)
        }
    }
}

func (w *CleanerWorker) tick(ctx context.Context) error {
    threshold := w.Now().Add(-w.AbandonAfter)
    rows, err := w.Store.SelectAbandonedQueued(ctx, threshold)
    if err != nil {
        return err
    }
    for _, r := range rows {
        w.Pool.CancelQueued(r.ID)
        _ = w.Store.MarkClosed(ctx, r.ID, "abandoned")
    }
    return nil
}
```

`SelectAbandonedQueued`:

```sql
-- name: SelectAbandonedQueued :many
SELECT * FROM streaming_sessions
 WHERE state = 'queued'
   AND closed_at IS NULL
   AND last_segment_at < $1
 ORDER BY started_at ASC
 LIMIT 256;
```

### 2.7 The gRPC `WatchQueue` server-streaming RPC

`WatchQueue` is canonical in architecture §9.9 (server-streaming). This
story owns wiring it on the server side; the proto file gets the new
RPC added here:

```proto
service StreamingService {
  rpc WatchQueue(WatchQueueRequest) returns (stream QueueUpdate);
}

message WatchQueueRequest { string session_id = 1; }
message QueueUpdate {
  int32 position = 1;
  int32 eta_sec  = 2;
  string state   = 3;        // 'queued' | 'active' | 'abandoned'
  string manifest_url = 4;   // populated on 'active' only
}
```

```go
// streaming/internal/grpcserver/watch_queue.go
package grpcserver

func (s *Server) WatchQueue(req *streamingv1.WatchQueueRequest, stream streamingv1.StreamingService_WatchQueueServer) error {
    sid, err := uuid.Parse(req.SessionId)
    if err != nil {
        return status.Errorf(codes.InvalidArgument, "invalid session_id")
    }
    ch, ok := s.Manager.PromotionChannel(sid)
    if !ok {
        return status.Errorf(codes.NotFound, "no queued session")
    }
    for u := range ch {
        out := &streamingv1.QueueUpdate{
            Position: int32(u.Position), EtaSec: int32(u.ETASec), State: u.State,
        }
        if u.State == "active" {
            out.ManifestUrl, _ = s.Manager.ManifestPath(sid)
        }
        if err := stream.Send(out); err != nil {
            return err
        }
        if u.State != "queued" {
            return nil
        }
    }
    return nil
}
```

The API subscribes once per queued session and bridges into a WebSocket
fanout to the client (Epic 7 territory).

## 3. Test plan

### 3.1 Pool unit tests (`pool_test.go`)

| Test | What it pins |
|---|---|
| `TestPool_AcquireBelowCapSucceeds` | `cap=4`, 4 sequential Acquires return slots; `Snapshot` shows used=4. |
| `TestPool_AcquireOverCapNoQueue_Full` | 5th Acquire with `AcceptQueue=false` and `CanDegradeToDirect=false` → ErrFull. AC-1. |
| `TestPool_DegradeToDirect720` | 5th Acquire with `CanDegradeToDirect=true` → ErrDegradeToDirect; caller switches mode. AC-2. |
| `TestPool_QueueAdmits` | 5th Acquire with `AcceptQueue=true` → ErrQueued; QueueUpdate sent on the promote channel with position=1. AC-3. |
| `TestPool_PromotionOnRelease` | Acquire 4 + queue 1 → Release one slot → queued slot promoted within 1 ms; QueueUpdate with state='active' sent. |
| `TestPool_FIFO` | Queue 3 sessions; Release 3 times; promotions happen in enqueue order. |
| `TestPool_CancelQueuedRemovesFromList` | Queue 3, cancel #2, release a slot → #1 promoted, #3 still queued. |
| `TestPool_SetCap_LowerDoesNotKill` | cap=4, used=4; SetCap(2); used remains 4 until Releases happen; new Acquires need used < 2. |
| `TestPool_SetCap_RaiseAdmitsQueued` | cap=4, used=4, queued=2; SetCap(6) → both queued promoted within tick. |
| `TestPool_ContextCancelDuringQueue` | Acquire → ErrQueued; ctx cancel; subsequent CancelQueued(sid) cleans without leaking the goroutine. |
| `TestPool_ETAEstimateReasonable` | EMA seeded with 60-s sessions; queued at position 3 → ETA ≈ 180 s ± 10%. |

### 3.2 Manager integration (`manager_test.go` extension)

| Test | What it pins |
|---|---|
| `TestManager_OpenQueuedRecordsRow` | Open with `accept_queue=true` against a full pool → row inserted with `state='queued'`, no FFmpeg spawned. |
| `TestManager_PromoteRow_SpawnFFmpegSetsActive` | Trigger promotion → row updated to `state='active'`, FFmpeg spawned, manifest_path set. |
| `TestManager_DegradeToDirect_Mode` | Acquire returns ErrDegradeToDirect → manager sets `mode='direct-degraded'` on the response and the row. AC-2. |
| `TestManager_OpenQueuedThenAPICloses` | API calls CloseSession on a queued row → CancelQueued + MarkClosed; pool size returns to its prior value. |

### 3.3 Cleaner tests (`cleaner_test.go`)

| Test | What it pins |
|---|---|
| `TestCleaner_RemovesQueuedSilentForLongerThanAbandonAfter` | Insert 3 queued rows; one is silent for 6 min; cleaner.tick → that row gone, the others kept; `queued_sessions_abandoned_total` ticks. |
| `TestCleaner_DoesNotTouchActive` | Active rows with old `last_segment_at` are reaper territory, not cleaner. |
| `TestCleaner_NoQueuedRows_NoOp` | Empty queue → tick is silent. |

### 3.4 End-to-end gRPC

| Test | What it pins |
|---|---|
| `TestE2E_OpenAcceptQueueReceivesPromotionUpdate` | gRPC OpenSession (full pool, accept_queue=true) → response state='queued', position=1; subscribe via WatchQueue; Release a slot → next QueueUpdate has state='active' and manifest_url set. AC-3 acceptance. |
| `TestE2E_5OpensOn4SlotHostQueuesOne_PromotedWithin5s` | Open 5 sessions in parallel; assert: 4 active, 1 queued; closing one promotes the queued within 5 s. AC story acceptance. |
| `TestE2E_DirectPlayUnaffectedByCap` | Pool full; open a direct-playable source → succeeds without consuming a slot. AC story acceptance. |

### 3.5 Stress

`TestStress_50OpenAcceptQueue_NoLeaks` — 50 OpenSessions on a 4-slot
host all with accept_queue=true; close all 50 in random order; final
state: pool.used == 0, pool.queue empty, no leaked goroutines.

## 4. Test code scaffolding

```go
// streaming/internal/slot/pool_test.go
package slot_test

import (
    "context"
    "sync"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/slot"
)

func newPromote() chan slot.QueueUpdate {
    return make(chan slot.QueueUpdate, 4)
}

func TestPool_AcquireOverCapNoQueue_Full(t *testing.T) {
    p := slot.New(2)

    s1, err := p.Acquire(context.Background(), slot.Request{SessionID: uuid.New(), Promote: newPromote()})
    require.NoError(t, err)
    s2, err := p.Acquire(context.Background(), slot.Request{SessionID: uuid.New(), Promote: newPromote()})
    require.NoError(t, err)

    _, err = p.Acquire(context.Background(), slot.Request{SessionID: uuid.New(), Promote: newPromote()})
    require.ErrorIs(t, err, slot.ErrFull)

    s1.Release()
    s2.Release()
}

func TestPool_QueueAdmits(t *testing.T) {
    p := slot.New(1)

    s1, err := p.Acquire(context.Background(), slot.Request{SessionID: uuid.New(), Promote: newPromote()})
    require.NoError(t, err)

    promote := newPromote()
    _, err = p.Acquire(context.Background(), slot.Request{
        SessionID: uuid.New(), AcceptQueue: true, Promote: promote,
    })
    require.ErrorIs(t, err, slot.ErrQueued)

    select {
    case u := <-promote:
        require.Equal(t, 1, u.Position)
        require.Equal(t, "queued", u.State)
    case <-time.After(100 * time.Millisecond):
        t.Fatal("no initial QueueUpdate")
    }

    s1.Release()

    select {
    case u := <-promote:
        require.Equal(t, "active", u.State)
    case <-time.After(time.Second):
        t.Fatal("no promotion update after release")
    }
}

func TestPool_SetCap_RaiseAdmitsQueued(t *testing.T) {
    p := slot.New(1)
    s, err := p.Acquire(context.Background(), slot.Request{SessionID: uuid.New(), Promote: newPromote()})
    require.NoError(t, err)

    promote := newPromote()
    _, err = p.Acquire(context.Background(), slot.Request{
        SessionID: uuid.New(), AcceptQueue: true, Promote: promote,
    })
    require.ErrorIs(t, err, slot.ErrQueued)

    p.SetCap(2)
    p.PumpQueueForTest() // exposed test helper that calls promoteHeadLocked

    select {
    case u := <-promote:
        require.Equal(t, "active", u.State)
    case <-time.After(time.Second):
        t.Fatal("queue not drained after SetCap raise")
    }
    _ = s
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Cap lowered at runtime via settings | Existing sessions are NOT killed; new Acquires respect the new cap. | `TestPool_SetCap_LowerDoesNotKill` |
| Queued client disconnects before promotion | Cleaner detects after AbandonAfter; calls CancelQueued + MarkClosed; `queued_sessions_abandoned_total` ticks. | `TestCleaner_RemovesQueuedSilentForLongerThanAbandonAfter` |
| Promotion channel listener slow / gone | `select { default }` drops the QueueUpdate; the row is still promoted, but the API/client may not see it. The next manifest fetch returns the active manifest. We tolerate one missed update. | Implicit via select-default; covered by manager test asserting row state. |
| User opens 5 sessions on a 4-slot host (5th queued) | The story's primary AC. | `TestE2E_5OpensOn4SlotHostQueuesOne_PromotedWithin5s` |
| Direct-playable source on a full-slot host | Direct play does not consume a slot — only transcode does. | `TestE2E_DirectPlayUnaffectedByCap` |
| `direct-degraded` mode | When verdict.Mode was transcode but slots full and `CanDegradeToDirect`, the response carries `mode='direct-degraded'`; the manifest_url is the direct-play URL with a 720p cap (the override is set on the SessionOverride). | `TestManager_DegradeToDirect_Mode` |
| Two sessions racing to promote | Mutex on promote; FIFO preserved. | `TestPool_FIFO` |
| Hwaccel sub-cap (NVENC consumer GPU = 3) | Story 8.7's `SessionConcurrencyCap`; slot.Pool consults hwaccel.Detected, picks software encoder for sessions over the sub-cap; but the slot count is unchanged. | Cross-link to Story 8.7. |
| The pool's `Promote` channel is unbuffered or full | We use buffered=4; if full, drop. The state of truth is the row, not the channel. | Implicit. |
| OpenSession in queued state, then API CloseSession | Manager calls `Pool.CancelQueued(sid)` + MarkClosed; the row is `closed_at=now, reason='api'`. Future Acquires see no leak. | `TestManager_OpenQueuedThenAPICloses` |
| Pool drained in parallel with new Acquires | Mutex serializes; no torn state. | `TestStress_50OpenAcceptQueue_NoLeaks`. |

## 6. Dependencies

No new top-level deps. Uses `container/list`, `sync`, `time`, the
existing `sessionstore` package, and gRPC server-streaming.

## 7. Acceptance checklist

**Slot accounting (story ACs)**
- [ ] AC-1: Effective cap = `max(1, cores/4)`; over-cap OpenSession (no queue, no degrade) → gRPC `RESOURCE_EXHAUSTED`.
- [ ] AC-2: Direct-eligible source → `mode='direct-degraded'` when slots full.
- [ ] AC-3: `accept_queue=true` records `state='queued'` row; promotion within 5 s of slot release; WatchQueue stream emits state='active' with manifest_url.

**Cleanup**
- [ ] Queued sessions silent > AbandonAfter are pruned by the cleaner; `queued_sessions_abandoned_total` ticks.
- [ ] Cleaner does not touch active sessions.

**Cap reload**
- [ ] `LISTEN streaming_settings_changed` triggers `Pool.SetCap`.
- [ ] Lowering does not kill existing sessions.
- [ ] Raising drains queued sessions on the next promote pump.

**Observability**
- [ ] Gauges: `slot_pool_total`, `slot_pool_used`, `slot_pool_queued`.
- [ ] Counters: `queued_sessions_promoted_total`, `queued_sessions_abandoned_total`.
- [ ] Histogram: `slot_wait_seconds` (time between enqueue and promote).

**Docs**
- [ ] Operator note: `[transcode] max_concurrent` accepts `0` for auto.
- [ ] `streaming/configs/streaming.toml.example` documents `[queue]` block.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.10.
