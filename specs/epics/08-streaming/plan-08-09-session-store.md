# Implementation Plan — Story 8.9 Session Store, Sticky Transcoder, Reaper

> Companion to [story-08-09-session-store.md](story-08-09-session-store.md).
> The story states *what* and *why*; this plan states *how*. Schema is
> defined in the [Epic 8 README](README.md). Consumed by
> [Story 8.8](plan-08-08-grpc-server.md) and feeds
> [Story 8.10](plan-08-10-concurrency-caps.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `shared/db/migrations/0020_streaming_sessions.sql` (Postgres) and `.sqlite.sql` variant. Numbering continues from Epic 6's `0010_processing_jobs.sql`. |
| Package | `streaming/internal/sessionstore` for the typed CRUD surface (sqlc-generated under the hood); `streaming/internal/reaper` for the periodic worker. |
| Heartbeat batching | Per-session `time.Ticker` at 5 s; the segment handler increments an in-memory counter, the ticker flushes one UPDATE for any session with pending counts. |
| Reaper interval | 30 s default. Idle threshold 90 s. Both configurable. |
| Cross-host | Sticky cookie `maktaba_streaming_host=<host_id>` set on the manifest response; L7 LB does consistent-hash on cookie value. Misrouted requests → `421 Misdirected Request` with `X-Streaming-Host` header. |
| Out of scope | Slot accounting (Story 8.10). Queue promotion (8.10). The actual signing of URLs (the API does that — the host hint is just a bare string). |

## 1. Architecture diagram

```
            ┌────────────────────────────────────────────────────┐
            │ streaming_sessions (Postgres)                       │
            │   id, video_id, user_id, client_profile,            │
            │   mode, format, host, pid,                          │
            │   started_at, last_segment_at,                      │
            │   closed_at, closed_reason, state                   │
            │ Indexes:                                            │
            │   - reaper:        (last_segment_at)  WHERE closed_at IS NULL │
            │   - user_video:    (user_id, video_id) WHERE closed_at IS NULL │
            └────────────────────────────────────────────────────┘
                          ▲              ▲           ▲
            Insert        │              │           │ SELECT FOR UPDATE SKIP LOCKED
            (Open 8.8)    │              │           │ + UPDATE closed_at, closed_reason
                          │              │           │
            ┌─────────────┴──────────────┴───────────┴─────────────┐
            │ sessionstore.Store                                    │
            │   .Insert(row)                                       │
            │   .Get(id)                                           │
            │   .ListActiveOnHost(host) — for crash recovery       │
            │   .HeartbeatBatch([(id, ts)...])                     │
            │   .MarkClosed(id, reason)                            │
            │   .ListIdle(threshold) → []Row                       │
            └────────────────────────────────────────────────────┘
                          ▲                       ▲
                          │                       │
                          │                       │
            ┌─────────────┴──────────┐ ┌──────────┴───────────────┐
            │ heartbeat.Recorder     │ │ reaper.Worker            │
            │  - 1 per host          │ │  - 30 s tick             │
            │  - ticker → flush      │ │  - SELECT FOR UPDATE     │
            │    UPDATE per dirty id │ │  - kill, purge, mark     │
            └────────────────────────┘ └──────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0020_streaming_sessions.sql` | Postgres migration (table, indexes). |
| `shared/db/migrations/0020_streaming_sessions.sqlite.sql` | SQLite variant. |
| `shared/db/queries/streaming_sessions.sql` | sqlc input. |
| `streaming/internal/sessionstore/store.go` | `Store` wrapper around sqlc-generated `Queries`. |
| `streaming/internal/sessionstore/store_test.go` | DB-level tests (parametrized over Postgres/SQLite). |
| `streaming/internal/sessionstore/heartbeat.go` | `Recorder` — collects (sid, ts) updates and flushes batches. |
| `streaming/internal/sessionstore/heartbeat_test.go` | Batching semantics. |
| `streaming/internal/reaper/reaper.go` | `Worker.Run(ctx)` — periodic reaper. |
| `streaming/internal/reaper/reaper_test.go` | End-to-end reaper tests. |
| `streaming/internal/server/sticky.go` | Cookie set on manifest response; 421 on misroute. |
| `streaming/internal/server/sticky_test.go` | Sticky-routing tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/internal/grpcserver/open_session.go` | Sets `host` to `cfg.HostID` on insert; populates response.Host. |
| `streaming/internal/hls/handler.go`, `streaming/internal/dash/handler.go`, `streaming/internal/handlers/manifest_dispatch.go` | Set sticky cookie when serving the manifest. Call `heartbeat.Recorder.Touch(sid)` from segment handlers. |
| `streaming/cmd/maktaba-streaming/main.go` | Start the reaper + heartbeat recorder; subscribe to host-shutdown SIGTERM to call `Recorder.Flush(ctx)`. |
| `streaming/internal/observability/metrics.go` | `sessions_active`, `sessions_reaped_idle_total`, `sessions_reaped_crash_total`, `heartbeat_flush_duration_seconds`, `heartbeat_flush_rows`. |
| `streaming/configs/streaming.toml.example` | `[reaper]` and `[heartbeat]` blocks. |
| `specs/epics/08-streaming/README.md` | Tick 8.9. |

### 2.3 Migration — Postgres

```sql
-- shared/db/migrations/0020_streaming_sessions.sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE streaming_sessions (
    id              UUID PRIMARY KEY,
    video_id        UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_profile  TEXT NOT NULL,
    mode            TEXT NOT NULL,
    format          TEXT NOT NULL,
    host            TEXT NOT NULL,
    pid             INTEGER,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_segment_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at       TIMESTAMPTZ,
    closed_reason   TEXT,
    state           TEXT NOT NULL DEFAULT 'active',

    CONSTRAINT streaming_sessions_mode_chk CHECK (
        mode IN ('direct','remux','transcode','direct-degraded')
    ),
    CONSTRAINT streaming_sessions_format_chk CHECK (
        format IN ('hls','dash')
    ),
    CONSTRAINT streaming_sessions_state_chk CHECK (
        state IN ('active','queued')
    ),
    CONSTRAINT streaming_sessions_closed_reason_chk CHECK (
        closed_reason IS NULL OR closed_reason IN
        ('api','idle','crash','evicted','user-stop','admin-evict',
         'hw_failed_software_failed','store-insert-failed')
    )
);

CREATE INDEX streaming_sessions_reaper
    ON streaming_sessions (last_segment_at)
    WHERE closed_at IS NULL;

CREATE INDEX streaming_sessions_user_video
    ON streaming_sessions (user_id, video_id)
    WHERE closed_at IS NULL;

-- Lookups by host for crash recovery on this binary's startup.
CREATE INDEX streaming_sessions_active_host
    ON streaming_sessions (host)
    WHERE closed_at IS NULL;

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS streaming_sessions;
```

The SQLite variant is identical except for the type swaps (UUID→TEXT,
TIMESTAMPTZ→TEXT) used in earlier epics.

### 2.4 sqlc queries — `shared/db/queries/streaming_sessions.sql`

```sql
-- name: InsertSession :exec
INSERT INTO streaming_sessions
  (id, video_id, user_id, client_profile, mode, format, host, pid, state)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetSession :one
SELECT * FROM streaming_sessions WHERE id = $1;

-- name: ListActiveOnHost :many
SELECT * FROM streaming_sessions
 WHERE host = $1 AND closed_at IS NULL;

-- name: BatchHeartbeat :exec
-- Per-session UPDATE; the Recorder loops over its dirty set and runs
-- one EXECUTE per id. We could use UPDATE FROM (VALUES ...) but the
-- per-row UPDATE is fast and keeps the SQL simple.
UPDATE streaming_sessions
   SET last_segment_at = $2
 WHERE id = $1 AND closed_at IS NULL;

-- name: MarkClosed :exec
UPDATE streaming_sessions
   SET closed_at = now(), closed_reason = $2
 WHERE id = $1 AND closed_at IS NULL;

-- name: SelectIdleForReap :many
-- The reaper picks rows whose last_segment_at is older than the
-- threshold AND that aren't already closed. SKIP LOCKED so two reapers
-- on different hosts don't block each other (the row-level lock is
-- short-lived per row anyway).
SELECT * FROM streaming_sessions
 WHERE closed_at IS NULL
   AND last_segment_at < $1
 ORDER BY last_segment_at ASC
 LIMIT 256
 FOR UPDATE SKIP LOCKED;
```

### 2.5 Heartbeat batching

```go
// streaming/internal/sessionstore/heartbeat.go
package sessionstore

import (
    "context"
    "sync"
    "time"

    "github.com/google/uuid"
)

// Recorder coalesces last_segment_at updates. The segment handler
// calls Touch(sid) on every served segment; Run flushes the dirty set
// every tickPeriod (default 5s). One UPDATE per dirty id per tick.
type Recorder struct {
    Store      *Store
    TickPeriod time.Duration

    mu    sync.Mutex
    dirty map[uuid.UUID]time.Time
}

func NewRecorder(s *Store, period time.Duration) *Recorder {
    return &Recorder{Store: s, TickPeriod: period, dirty: map[uuid.UUID]time.Time{}}
}

func (r *Recorder) Touch(sid uuid.UUID) {
    now := time.Now().UTC()
    r.mu.Lock()
    if existing, ok := r.dirty[sid]; !ok || now.After(existing) {
        r.dirty[sid] = now
    }
    r.mu.Unlock()
}

func (r *Recorder) Run(ctx context.Context) error {
    t := time.NewTicker(r.TickPeriod)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return r.Flush(context.Background())
        case <-t.C:
            if err := r.Flush(ctx); err != nil {
                return err
            }
        }
    }
}

func (r *Recorder) Flush(ctx context.Context) error {
    r.mu.Lock()
    snap := r.dirty
    r.dirty = make(map[uuid.UUID]time.Time, len(snap))
    r.mu.Unlock()

    if len(snap) == 0 {
        return nil
    }
    start := time.Now()
    n := 0
    for sid, ts := range snap {
        if err := r.Store.BatchHeartbeat(ctx, sid, ts); err != nil {
            return err
        }
        n++
    }
    flushDuration.Observe(time.Since(start).Seconds())
    flushRows.Add(float64(n))
    return nil
}
```

### 2.6 Reaper

```go
// streaming/internal/reaper/reaper.go
package reaper

import (
    "context"
    "errors"
    "log/slog"
    "time"

    "github.com/google/uuid"

    "maktaba/streaming/internal/sessionstore"
    "maktaba/streaming/internal/session"
)

type Worker struct {
    Store         *sessionstore.Store
    Manager       *session.Manager   // for tearing down local sessions
    HostID        string
    Tick          time.Duration      // 30s default
    IdleAfter     time.Duration      // 90s default
    Now           func() time.Time   // injectable for tests
}

func (w *Worker) Run(ctx context.Context) error {
    if w.Now == nil { w.Now = time.Now }
    t := time.NewTicker(w.Tick)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-t.C:
            if err := w.tick(ctx); err != nil {
                slog.WarnContext(ctx, "reaper.tick", "err", err)
            }
        }
    }
}

func (w *Worker) tick(ctx context.Context) error {
    threshold := w.Now().Add(-w.IdleAfter)
    rows, err := w.Store.SelectIdleForReap(ctx, threshold)
    if err != nil {
        return err
    }
    for _, r := range rows {
        reason := "idle"
        if r.Host != w.HostID {
            // Cross-host reap: we can mark the row but cannot kill the
            // FFmpeg on the other box (no IPC). The owning host's reaper
            // will catch its own. We only mark when the row's host has
            // been silent for 2× the idle window — i.e., its host is
            // probably down.
            if r.LastSegmentAt.Add(2 * w.IdleAfter).After(w.Now()) {
                continue
            }
            reason = "crash"
        }

        if r.Host == w.HostID {
            // Local: fully tear down.
            _ = w.Manager.Close(ctx, r.ID, reason)
        } else {
            // Cross-host marker only.
            if err := w.Store.MarkClosed(ctx, r.ID, reason); err != nil {
                slog.WarnContext(ctx, "reaper.mark-closed", "err", err, "session_id", r.ID)
                continue
            }
        }

        if reason == "idle" {
            metricsReapedIdle.Inc()
        } else {
            metricsReapedCrash.Inc()
        }
    }
    return nil
}
```

### 2.7 Sticky routing

```go
// streaming/internal/server/sticky.go
package server

const stickyCookieName = "maktaba_streaming_host"

// EmitSticky sets the cookie on every manifest response. The L7 LB is
// configured (out-of-band) to consistent-hash on this cookie. For
// localhost installs, the LB is absent and the cookie is harmless.
func EmitSticky(w http.ResponseWriter, hostID string) {
    http.SetCookie(w, &http.Cookie{
        Name:     stickyCookieName,
        Value:    hostID,
        Path:     "/stream/",
        HttpOnly: true,
        Secure:   true,
        SameSite: http.SameSiteLaxMode,
        MaxAge:   3600,
    })
}

// StickyGuard returns 421 Misdirected Request when a segment lands on
// a different host than the session was opened on.
func StickyGuard(localHostID string, store *sessionstore.Store) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            sid := chi.URLParam(r, "session_id")
            if sid == "" {
                next.ServeHTTP(w, r)
                return
            }
            row, err := store.Get(r.Context(), uuid.MustParse(sid))
            if err != nil {
                next.ServeHTTP(w, r) // let the segment handler 404
                return
            }
            if row.Host != localHostID {
                w.Header().Set("X-Streaming-Host", row.Host)
                httpx.Write(w, http.StatusMisdirectedRequest,
                    "misdirected-request",
                    "session is owned by a different host",
                    row.Host)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

## 3. Test plan

### 3.1 Migration / store tests (`store_test.go`)

Run parametrized over Postgres + SQLite via the dialect fixture from
Epic 6's plan.

| Test | What it pins |
|---|---|
| `TestMigration_TableAndIndexesExist` | After migration up, `streaming_sessions` has columns from the README + 3 indexes (`reaper`, `user_video`, `active_host`). |
| `TestMigration_StateAndModeCheckConstraints` | INSERT with `mode='garbage'` raises CHECK; `mode='direct-degraded'` succeeds (the AC of Story 8.10). |
| `TestStore_Insert_Get_RoundTrip` | InsertSession + GetSession returns the same row; `closed_at` is NULL. |
| `TestStore_ListActiveOnHost` | Inserts on host A and host B → ListActiveOnHost(A) returns only A's rows. |
| `TestStore_BatchHeartbeat_UpdatesLastSegmentAt` | Heartbeat with `now+1m` advances the row; older heartbeat leaves the value alone. |
| `TestStore_MarkClosed_Idempotent` | Two MarkClosed calls → both succeed; closed_at + reason set on first; second is a no-op (the WHERE-clause keeps it OK). |
| `TestStore_CascadeOnVideoDelete` | DELETE from videos cascades; the row in streaming_sessions is gone. |
| `TestStore_CascadeOnUserDelete` | Same for users. |
| `TestStore_SelectIdleForReap_RespectsThreshold` | Insert two rows; bump one's `last_segment_at` to "now"; SelectIdleForReap returns only the older one. |
| `TestStore_SelectIdleForReap_SkipLocked` | Two transactions hold rows: A holds row 1; B's SelectIdleForReap returns rows 2..N and skips 1. |

### 3.2 Heartbeat (`heartbeat_test.go`)

| Test | What it pins |
|---|---|
| `TestRecorder_BatchesAcrossTicks` | 100 Touch(sid) calls within 1 s → exactly 1 BatchHeartbeat call after the next tick. AC-2. |
| `TestRecorder_FlushOnContextCancel` | Pending dirty set + ctx cancel → Flush runs once with the snapshot before Run returns. |
| `TestRecorder_LatestTimestampWins` | Touch with t1 then t1-100ms → only t1 is flushed. |
| `TestRecorder_DBErrorPropagates` | Mock store returns error → Run returns the error; the dirty set is not lost (re-added). |

### 3.3 Reaper (`reaper_test.go`)

Uses `sqlite_db` for fast iteration; one Postgres-backed test for the SKIP LOCKED behavior.

| Test | What it pins |
|---|---|
| `TestReaper_KillsIdleLocalSession` | Session on this host with `last_segment_at = now-91s` → reaper.tick → Manager.Close called; row closed `reason='idle'`. AC-3. |
| `TestReaper_FreshSessionUntouched` | Session with `last_segment_at = now-30s` → tick is a no-op. |
| `TestReaper_CrossHostStaleSession_MarksClosedOnly` | Session on host B, `last_segment_at = now-200s` (> 2× idle). Reaper on host A marks `reason='crash'`. Manager.Close NOT called for that session (cross-host). |
| `TestReaper_CrossHostFreshSession_NotTouched` | Session on host B, `last_segment_at = now-100s` → reaper on A skips (within the 2× window). |
| `TestReaper_TickResilientToTransientDB` | Mock store fails once then succeeds → reaper.tick logs warning, next tick proceeds. |
| `TestReaper_KillsAllAtShutdown` | SIGTERM during a tick → in-flight close completes; subsequent ticks don't run. |

### 3.4 Sticky integration (`sticky_test.go`)

| Test | What it pins |
|---|---|
| `TestSticky_CookieSetOnManifest` | GET manifest returns `Set-Cookie: maktaba_streaming_host=<host_id>; Path=/stream/; ...`. AC-4. |
| `TestSticky_MisroutedRequestReturns421` | Manifest minted on host A, segment sent to host B → 421 with `X-Streaming-Host: <hostA>`. |
| `TestSticky_OwnHostPasses200` | Same host as the row → handler proceeds to segment serving. |
| `TestSticky_NoSessionId_PassesThrough` | Direct-play route (no `{session_id}` param) → no-op. |

### 3.5 End-to-end timing

| Test | What it pins |
|---|---|
| `TestE2E_HeartbeatBatchSize_100Per1s` | 100 segment requests in 1 s for one session → 1 UPDATE on the next 5 s tick. AC-2 acceptance. |
| `TestE2E_ReaperKillsIdleWithin30sOfThreshold` | Force `last_segment_at = now-91s`, run reaper; within 30 s of the threshold cross, the row is closed. AC-3 acceptance. |
| `TestE2E_ClosedSessionDirGoneWithin1s` | Close → cache/hls/{sid} gone within 1 s (verified via os.Stat polling). AC story acceptance. |

## 4. Test code scaffolding

```go
// streaming/internal/sessionstore/heartbeat_test.go
package sessionstore_test

import (
    "context"
    "sync/atomic"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/stretchr/testify/require"
)

type spyStore struct {
    calls atomic.Int64
}

func (s *spyStore) BatchHeartbeat(_ context.Context, _ uuid.UUID, _ time.Time) error {
    s.calls.Add(1)
    return nil
}

func TestRecorder_BatchesAcrossTicks(t *testing.T) {
    spy := &spyStore{}
    r := sessionstore.NewRecorder(spy, 50*time.Millisecond)

    sid := uuid.New()
    for i := 0; i < 100; i++ {
        r.Touch(sid)
    }
    require.NoError(t, r.Flush(context.Background()))
    require.Equal(t, int64(1), spy.calls.Load())
}

func TestRecorder_FlushOnContextCancel(t *testing.T) {
    spy := &spyStore{}
    r := sessionstore.NewRecorder(spy, time.Hour) // no tick during test

    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    go func() { done <- r.Run(ctx) }()

    r.Touch(uuid.New())
    cancel()
    require.NoError(t, <-done)
    require.Equal(t, int64(1), spy.calls.Load())
}
```

```go
// streaming/internal/reaper/reaper_test.go
func TestReaper_KillsIdleLocalSession(t *testing.T) {
    h := newReaperHarness(t)
    sid := h.InsertSession(t, "host-a", time.Now().Add(-91*time.Second))

    w := &reaper.Worker{
        Store: h.Store, Manager: h.Manager,
        HostID: "host-a", Tick: time.Millisecond, IdleAfter: 90*time.Second,
        Now: func() time.Time { return time.Now() },
    }
    require.NoError(t, w.RunOnce(context.Background()))

    row := h.GetSession(t, sid)
    require.NotNil(t, row.ClosedAt)
    require.Equal(t, "idle", *row.ClosedReason)
    require.Equal(t, 1, h.Manager.CloseCalls(sid))
}

func TestReaper_CrossHostStaleSession_MarksClosedOnly(t *testing.T) {
    h := newReaperHarness(t)
    sid := h.InsertSession(t, "host-b", time.Now().Add(-200*time.Second))

    w := &reaper.Worker{
        Store: h.Store, Manager: h.Manager,
        HostID: "host-a", Tick: time.Millisecond, IdleAfter: 90*time.Second,
        Now: func() time.Time { return time.Now() },
    }
    require.NoError(t, w.RunOnce(context.Background()))

    row := h.GetSession(t, sid)
    require.NotNil(t, row.ClosedAt)
    require.Equal(t, "crash", *row.ClosedReason)
    require.Equal(t, 0, h.Manager.CloseCalls(sid)) // never killed locally
}
```

## 5. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Owning Streaming binary crashed | Cross-host reaper marks `reason='crash'` once `last_segment_at` is older than 2×idle. Local cache cleanup is skipped (each box cleans its own; see Story 8.14 for orphan dir GC). | `TestReaper_CrossHostStaleSession_MarksClosedOnly` |
| Player paused > 90 s | Session reaped; on resume, the player gets 401 (or 404) on the next segment and reopens the session via API. We document the auto-reopen behavior in the web client epic. | Reap test + HTTP integration. |
| `last_segment_at` write colliding with reaper read | Reaper uses `FOR UPDATE SKIP LOCKED`; a fresh write between read and update simply means the next tick picks it up. | `TestStore_SelectIdleForReap_SkipLocked` |
| Heartbeat race on close | `BatchHeartbeat` has `WHERE closed_at IS NULL`; once `MarkClosed` lands, late heartbeats are no-ops. | `TestStore_BatchHeartbeat_AfterCloseNoOp` |
| Ticker hasn't fired but binary SIGTERMed | `Recorder.Run` calls `Flush(context.Background())` before returning. Pending dirty rows are persisted. | `TestRecorder_FlushOnContextCancel` |
| Player switches hosts mid-session | 421 Misdirected Request with `X-Streaming-Host` so the LB can re-route. | `TestSticky_MisroutedRequestReturns421` |
| Multi-tenant LB without cookie support | Sticky cookie still set; LB falls back to source-IP hashing (out of band). The 421 path catches misroutes either way. | Documented; no test (LB-side concern). |
| `streaming_sessions` row deleted by `videos` ON DELETE CASCADE | The reaper's next SELECT skips the row; nothing to clean up server-side because the DB already removed it. | Implicit; covered by `TestStore_CascadeOnVideoDelete`. |
| Reaper crashes mid-tick | Next tick re-runs SELECT FOR UPDATE; rows that were locked by the dead transaction are released by Postgres on connection close. SQLite uses `BEGIN IMMEDIATE` so the lock is per-statement; the same retry logic applies. | Implicit. |
| `pid` column out of date | `pid` is informational only; we never use it as the source of truth for "is this process alive." Reaper relies on `last_segment_at` only. | Documented in the schema comment. |
| Two reapers on the same host | They serialize on the row-level lock; idempotent. | Stress test (not in main table). |

## 6. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `github.com/jackc/pgx/v5` | from 8.1 | Already a dep. |
| sqlc | dev-only | Generates the typed `Queries`. |

## 7. Acceptance checklist

**Schema (story ACs)**
- [ ] AC-1: `streaming_sessions` columns match the README; 3 indexes present (reaper, user_video, active_host).
- [ ] CASCADE chain `libraries → videos → streaming_sessions` works (proven via `TestStore_CascadeOnVideoDelete`).
- [ ] `mode` CHECK accepts `direct-degraded`.

**Heartbeat**
- [ ] AC-2: 100 segment fetches in 1 s produce exactly 1 UPDATE on the next 5 s flush.
- [ ] Pending dirty set is flushed on context cancel.
- [ ] Heartbeat after close is a no-op.

**Reaper**
- [ ] AC-3: Local idle session is killed within 30 s of crossing the 90 s threshold; reason='idle'.
- [ ] Cross-host stale session (>2×idle) is marked `reason='crash'`; local cleanup not attempted.
- [ ] Reaper ticks resilient to transient DB errors (logged warning, next tick proceeds).

**Sticky routing**
- [ ] AC-4: Manifest response sets `Set-Cookie: maktaba_streaming_host=...`.
- [ ] Misdirected segment request → 421 with `X-Streaming-Host: <owner>`.
- [ ] Same-host request passes through to segment handler.

**Cleanup**
- [ ] Closed session's per-session dir gone within 1 s.

**Observability**
- [ ] Counters: `sessions_reaped_idle_total`, `sessions_reaped_crash_total`, `heartbeat_flush_rows`, `heartbeat_flush_duration_seconds`.

**Docs**
- [ ] `streaming/configs/streaming.toml.example` documents `[reaper]` (interval, idle_after) and `[heartbeat]` (period).
- [ ] `specs/epics/08-streaming/README.md` ticks 8.9.
