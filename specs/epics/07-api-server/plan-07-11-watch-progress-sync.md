# Implementation Plan — Story 7.11 Watch Progress Sync

> Companion to [story-07-11-watch-progress-sync.md](story-07-11-watch-progress-sync.md).
> Drives resume-everywhere UX. Debounced upserts + WS fan-out via Story 7.16.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `POST /api/stream/sessions/{id}/progress` (per-session writer used by the player), `PATCH /api/me/playback-state {video_id, position_sec, completed?}` (session-less writer used by share-target/import flows and the iOS Now-Playing intent), `GET /api/users/me/playback/{video_id}` (read-back). The two writers funnel into the same coarsener and the same `playback_state` upsert path. |
| Storage | `playback_state(user_id, video_id)` (existing schema; this story adds an index). |
| Debounce | Per-session 1 Hz coarsening lives in-process; the HTTP returns 200 immediately. |
| Fan-out | Inserts an `events` row + Postgres `NOTIFY playback.progress` per persisted update; Story 7.16 listens. |
| Out of scope | The WS itself (Story 7.16), the `events` table schema (Epic 19), the streaming session schema (Story 7.10). |

## 1. Architecture diagram

```
   POST /api/stream/sessions/{id}/progress
   { position_sec, completed? }
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ 1. Validate session_id belongs to caller                    │
   │ 2. Look up video_id, duration_sec from session row          │
   │ 3. Clamp position_sec ∈ [0, duration_sec]                   │
   │ 4. Auto-set completed if position/duration > 0.95           │
   │ 5. coarsener.Submit(session_id, snap)                        │
   │     - keeps last snapshot per session                       │
   │     - flushes ≤ once per second per session                 │
   │ 6. Return 200 with {accepted: true}                         │
   └────────────────────────────────────────────────────────────┘

   coarsener flush goroutine (per-process)
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ For each session due:                                      │
   │   tx:                                                      │
   │     INSERT INTO playback_state (user_id, video_id,         │
   │                                  position_sec, completed,   │
   │                                  updated_at, source_session)│
   │     ON CONFLICT (user_id, video_id) DO UPDATE              │
   │       SET position_sec = EXCLUDED.position_sec,            │
   │           completed    = playback_state.completed           │
   │                          OR EXCLUDED.completed,             │
   │           updated_at   = EXCLUDED.updated_at,               │
   │           source_session_id = EXCLUDED.source_session_id;   │
   │     INSERT INTO events (channel, payload)                  │
   │       VALUES ('playback.progress', ...);                   │
   │     pg_notify('playback.progress', payload);                │
   │   if completed flipped: NOTIFY 'playback.completed';        │
   └────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/playback/handler.go` | `POST /progress`, optional GET. |
| `api/internal/playback/coarsener.go` | In-process 1 Hz debouncer. |
| `api/internal/playback/types.go` | DTOs. |
| `api/internal/playback/handler_test.go` | Integration. |
| `api/internal/playback/coarsener_test.go` | Unit. |
| `shared/db/queries/playback_state.sql` | sqlc inputs. |
| `shared/db/migrations/0017_playback_state_indexes.sql` | `(user_id, updated_at DESC)` for "Continue Watching" reads. |

## 3. SQL — schema additions

`shared/db/migrations/0017_playback_state_indexes.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE playback_state
  ADD COLUMN IF NOT EXISTS source_session_id UUID;

CREATE INDEX IF NOT EXISTS playback_state_user_updated_idx
    ON playback_state (user_id, updated_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS playback_state_user_updated_idx;
ALTER TABLE playback_state DROP COLUMN IF EXISTS source_session_id;
-- +goose StatementEnd
```

## 4. Type definitions

```go
// api/internal/playback/types.go
package playback

import "time"
import "github.com/google/uuid"

type ProgressInput struct {
    PositionSec float64 `json:"position_sec" validate:"required,gte=0"`
    Completed   *bool   `json:"completed,omitempty"`
}

type ProgressEvent struct {
    Type            string    `json:"type"`           // "playback.progress" | "playback.completed"
    UserID          uuid.UUID `json:"user_id"`
    VideoID         uuid.UUID `json:"video_id"`
    PositionSec     float64   `json:"position_sec"`
    Completed       bool      `json:"completed"`
    SourceSessionID uuid.UUID `json:"source_session_id"`
    At              time.Time `json:"at"`
}
```

## 5. Coarsener

```go
// api/internal/playback/coarsener.go
package playback

import (
    "context"
    "sync"
    "time"
)

type snap struct {
    UserID, VideoID, SessionID uuid.UUID
    Position float64
    Completed bool
    At time.Time
}

type Coarsener struct {
    mu      sync.Mutex
    pending map[coarsenKey]snap // keyed by session_id when present, else (user_id, video_id)
    flush   func(context.Context, []snap)
    period  time.Duration       // 1 s
    quit    chan struct{}
}

// coarsenKey allows the session-less PATCH /api/me/playback-state to
// coalesce per (user, video) without colliding with other users' Nil
// session IDs.
type coarsenKey struct {
    SessionID uuid.UUID
    UserID    uuid.UUID
    VideoID   uuid.UUID
}

func NewCoarsener(flush func(context.Context, []snap), period time.Duration) *Coarsener {
    c := &Coarsener{pending: map[coarsenKey]snap{}, flush: flush, period: period, quit: make(chan struct{})}
    go c.loop()
    return c
}

func (c *Coarsener) Submit(s snap) {
    c.mu.Lock(); defer c.mu.Unlock()
    key := coarsenKey{SessionID: s.SessionID, UserID: s.UserID, VideoID: s.VideoID}
    c.pending[key] = s   // last-write-wins per (session OR user+video)
}

func (c *Coarsener) loop() {
    t := time.NewTicker(c.period)
    defer t.Stop()
    for {
        select {
        case <-t.C:
            c.flushOnce()
        case <-c.quit:
            c.flushOnce()
            return
        }
    }
}

func (c *Coarsener) flushOnce() {
    c.mu.Lock()
    if len(c.pending) == 0 { c.mu.Unlock(); return }
    batch := make([]snap, 0, len(c.pending))
    for _, s := range c.pending { batch = append(batch, s) }
    c.pending = map[coarsenKey]snap{}
    c.mu.Unlock()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    c.flush(ctx, batch)
}

func (c *Coarsener) Stop() { close(c.quit) }
```

## 6. Handler scaffolding

```go
// api/internal/playback/handler.go
package playback

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
)

func (h *handler) progress(w http.ResponseWriter, r *http.Request) {
    sid, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    var in ProgressInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    user := userFromCtx(r.Context())

    sess, err := h.db.GetSessionMeta(r.Context(), GetSessionMetaParams{ID: sid, UserID: user.ID})
    if errors.Is(err, sql.ErrNoRows) {
        httperror.Write(w, r, httperror.NotFound("session")); return
    }
    if err != nil { httperror.Write(w, r, httperror.Internal("session lookup")); return }

    pos := in.PositionSec
    if pos < 0 { pos = 0 }
    warned := false
    if sess.DurationSec > 0 && pos > sess.DurationSec {
        pos = sess.DurationSec
        warned = true
    }
    completed := pos/sess.DurationSec > 0.95
    if in.Completed != nil { completed = *in.Completed }

    h.coarsener.Submit(snap{
        UserID: user.ID, VideoID: sess.VideoID, SessionID: sid,
        Position: pos, Completed: completed,
        At: h.clock(),
    })

    if warned { w.Header().Set("Maktaba-Warning", "position-sec-clamped") }
    w.WriteHeader(http.StatusOK)
    _ = json.NewEncoder(w).Encode(map[string]any{"accepted": true})
}

// PATCH /api/me/playback-state {video_id, position_sec, completed?}
// Same write path as the streaming-session progress endpoint, but
// session-less. The coarsener key is the video_id (not a session_id) so
// concurrent writes from different surfaces still coalesce per video.
type MePlaybackInput struct {
    VideoID     uuid.UUID `json:"video_id"     validate:"required"`
    PositionSec float64   `json:"position_sec" validate:"required,gte=0"`
    Completed   *bool     `json:"completed,omitempty"`
}

func (h *handler) mePlayback(w http.ResponseWriter, r *http.Request) {
    var in MePlaybackInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if err := validate(in); err != nil { httperror.Write(w, r, err); return }
    user := userFromCtx(r.Context())

    dur, err := h.db.GetVideoDuration(r.Context(), in.VideoID)
    if errors.Is(err, sql.ErrNoRows) {
        httperror.Write(w, r, httperror.NotFound("video")); return
    }
    if err != nil { httperror.Write(w, r, httperror.Internal("video lookup")); return }

    pos := in.PositionSec
    warned := false
    if dur > 0 && pos > dur { pos = dur; warned = true }
    completed := dur > 0 && pos/dur > 0.95
    if in.Completed != nil { completed = *in.Completed }

    // SourceSessionID is uuid.Nil for session-less writes — that's fine,
    // the events fan-out treats Nil as "no source".
    h.coarsener.Submit(snap{
        UserID: user.ID, VideoID: in.VideoID, SessionID: uuid.Nil,
        Position: pos, Completed: completed, At: h.clock(),
    })
    if warned { w.Header().Set("Maktaba-Warning", "position-sec-clamped") }
    w.WriteHeader(http.StatusOK)
    _ = json.NewEncoder(w).Encode(map[string]any{"accepted": true})
}
```

The flush callback runs the upsert + NOTIFY:

```go
func (s *service) flush(ctx context.Context, batch []snap) {
    for _, snp := range batch {
        prev, err := s.db.PlaybackBefore(ctx, snp.UserID, snp.VideoID)
        if err != nil { /* log, skip */ continue }
        completedFlip := !prev.Completed && snp.Completed

        if err := s.db.UpsertPlaybackState(ctx, UpsertPlaybackStateParams{
            UserID: snp.UserID, VideoID: snp.VideoID,
            PositionSec: snp.Position, Completed: snp.Completed,
            UpdatedAt: snp.At, SourceSessionID: snp.SessionID,
        }); err != nil { continue }

        evt := ProgressEvent{
            Type: "playback.progress",
            UserID: snp.UserID, VideoID: snp.VideoID,
            PositionSec: snp.Position, Completed: snp.Completed,
            SourceSessionID: snp.SessionID, At: snp.At,
        }
        s.bus.Publish(ctx, "playback.progress", evt)

        if completedFlip {
            evt2 := evt; evt2.Type = "playback.completed"
            s.bus.Publish(ctx, "playback.completed", evt2)
        }
    }
}
```

## 7. SQL — sqlc inputs

`shared/db/queries/playback_state.sql`:

```sql
-- name: GetSessionMeta :one
SELECT s.video_id, COALESCE(v.duration_sec, 0) AS duration_sec
  FROM streaming_sessions s
  JOIN videos v ON v.id = s.video_id
 WHERE s.id = $1 AND s.user_id = $2;

-- name: GetVideoDuration :one
SELECT COALESCE(duration_sec, 0) AS duration_sec
  FROM videos WHERE id = $1 AND deleted_at IS NULL;

-- name: UpsertPlaybackState :exec
INSERT INTO playback_state
       (user_id, video_id, position_sec, completed,
        updated_at, source_session_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, video_id) DO UPDATE
   SET position_sec      = EXCLUDED.position_sec,
       completed         = playback_state.completed OR EXCLUDED.completed,
       updated_at        = EXCLUDED.updated_at,
       source_session_id = EXCLUDED.source_session_id;

-- name: PlaybackBefore :one
SELECT position_sec, completed FROM playback_state
 WHERE user_id = $1 AND video_id = $2;
```

## 8. Test plan

### 8.1 Unit (`coarsener_test.go`)

| Test | What it pins |
|---|---|
| `TestCoarsenerFlushPerSecond` | 10 Submit calls in 100 ms → flush fires once with the latest snapshot. |
| `TestCoarsenerSeparatesSessions` | Submit for two session ids → flush includes both, not coarsened across. |
| `TestCoarsenerFlushOnStop` | Submit then `Stop()` → flush invoked once with the pending snap. |

### 8.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestProgressPersists` | POST `{position_sec: 120}` → after the next coarsener tick, `playback_state` row has position 120. |
| `TestProgressDebounce` | 10 POSTs in 1 s → exactly 1 DB upsert. |
| `TestProgressFanout` | Two devices subscribed to `playback.progress` → both receive an event with the upserted position; `source_session_id` matches the POSTing session. |
| `TestProgressCompletedAuto` | POST with `position_sec/duration > 0.95` → `completed=true`; a `playback.completed` NOTIFY fires (only once across multiple POSTs). |
| `TestProgressStaleAccepted` | Stored 450; POST 200 → upsert stores 200, no monotonicity error. |
| `TestProgressClampsExceeding` | POST `{position_sec: duration+10}` → stored as `duration`; `Maktaba-Warning: position-sec-clamped` header. |
| `TestProgressPostAfterClose` | DELETE session, POST progress → 200 (the watch happened); `playback_state` updated; session's row is unchanged. |
| `TestRateLimitOptOut` | The progress route is excluded from the global per-user rate limit; 600 POSTs/min do not 429. (Story 7.19 owns the exception.) |
| `TestBulkReplay` | 30 POSTs queued via a fake offline-replay → 30 are accepted; 30 NOTIFYs fire after coarsening (one per second over ~30 s). |

## 9. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| User scrubs backwards | Lower position is accepted; not blocked. | `TestProgressStaleAccepted` |
| POST after `DELETE /sessions/{id}` | Accepted; `playback_state` keyed on `(user_id, video_id)` not session, so the write succeeds. | `TestProgressPostAfterClose` |
| `position_sec > duration_sec` | Clamped + warning header. | `TestProgressClampsExceeding` |
| Network jitter delivers POSTs out of order | Server uses its own clock for `updated_at`; "latest received" wins. | Documented |
| Disconnected client bulk-replays 30 progress POSTs | Rate-limited via the 1 Hz debouncer; final position is correct, intermediates coarsened. | `TestBulkReplay` |
| `completed=true` flips false→true → fires `playback.completed` once | The flush callback compares `prev.Completed` vs the incoming snap. Subsequent POSTs with completed already true do NOT re-fire. | `TestProgressCompletedAuto` |
| Session belongs to another user | Lookup returns `ErrNoRows` → 404 (not 403, to avoid leaking session existence). | Integration |
| Process crash before flush | The pending snap is lost (acceptable — the next POST will refresh). Document that durability is "≤ 1 s of staleness." | Documented |
| `playback_state` has `completed=true` and a later POST with completed implicit `false` | The upsert preserves true via `OR EXCLUDED.completed`. The user cannot un-complete a video via progress sync (intentional). | Integration |

## 10. Acceptance checklist

- [ ] POST persists with 1 Hz debounce (per session).
- [ ] Auto-`completed` when `position/duration > 0.95`; only one `playback.completed` event per transition.
- [ ] Stale POSTs accepted (no monotonicity check).
- [ ] WS fan-out (covered by Story 7.16) carries `source_session_id`.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.11.
