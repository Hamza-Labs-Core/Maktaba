# Implementation Plan — Story 29.1 Watch session tracking

> Companion to [story-29-01-watch-session-tracking.md](story-29-01-watch-session-tracking.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | Slot **0086** (`shared/db/migrations/0086_watch_sessions.sql` + `.sqlite.sql`): `watch_sessions` + `user_analytics_prefs`. |
| Package | `api/internal/handlers/watch/` (handler + repo + pure logic + reaper). |
| Routes | `POST /api/watch/start`, `/heartbeat`, `/stop`, mounted in `router/p29.go`. |
| Reaper | `watch.Reaper` started by `main.go` (mirrors `runPairingSweep`). |
| Auth | Authenticated principal required; session rows are owner-scoped. |

## 1. Migration (slot 0086)

`watch_sessions` and `user_analytics_prefs` per the README data model.
Postgres: `-- +goose NO TRANSACTION`, `CREATE TABLE IF NOT EXISTS`,
`CREATE INDEX CONCURRENTLY IF NOT EXISTS`. Indexes:

- `watch_sessions_user_started_idx (user_id, started_at DESC)`
- `watch_sessions_active_idx (last_heartbeat) WHERE state='active'` (live view + reaper)
- `watch_sessions_video_idx (video_id)`
- `watch_sessions_started_idx (started_at)` (time series + purge)

SQLite sibling: same tables/indexes without `CONCURRENTLY`/`NO TRANSACTION`,
`state` CHECK inline, timestamps as TEXT (`datetime('now')`).

## 2. Pure logic (`logic.go`, no DB — the unit-tested core)

```go
const (
    StateActive      = "active"
    StateCompleted   = "completed"
    StateStopped     = "stopped"
    StateInterrupted = "interrupted"
    DefaultStaleTimeout = 5 * time.Minute
    HeartbeatInterval   = 30 * time.Second
)

// PercentComplete clamps position/duration into 0..100.
func PercentComplete(positionSec, durationSec float64) float64

// CreditedSeconds returns watched time to add for a heartbeat: the live
// gap since lastHeartbeat, clamped to [0, staleTimeout] (guards clock
// jumps and long pauses — D3).
func CreditedSeconds(prev, now time.Time, staleTimeout time.Duration) int

// StopState maps a final percent to completed|stopped (≥95 ⇒ completed).
func StopState(percent float64) string

// IsStale reports whether an active session with lastHeartbeat should be
// reaped at now given staleTimeout.
func IsStale(lastHeartbeat, now time.Time, staleTimeout time.Duration) bool
```

These four are the lifecycle/interrupted-detection logic the tests
target (the repo's convention: DB-free handler tests — see
`streaming.SessionDebouncer`).

## 3. Repo (`repo.go`)

```go
func (r *repo) insertStart(ctx, s sessionRow) error
func (r *repo) loadActive(ctx, id, userID string) (sessionRow, videoDur, error) // owner-scoped
func (r *repo) applyHeartbeat(ctx, id string, lastHB time.Time, addSec int, pct float64) error
func (r *repo) stop(ctx, id string, endedAt time.Time, state string, pct float64, addSec int) error
func (r *repo) reapStale(ctx, cutoff time.Time) (int64, error)   // UPDATE … WHERE state='active' AND last_heartbeat < cutoff
func (r *repo) purgeOlderThan(ctx, cutoff time.Time) (int64, error)
func (r *repo) trackingEnabled(ctx, userID string) (bool, error) // absent row ⇒ true
```

`$N` placeholders (PG + SQLite). Stop uses
`WHERE id=$1 AND state='active'` so a second stop affects 0 rows and we
return the already-closed row (idempotency).

## 4. Handler (`watch.go`)

- `Start`: principal → `trackingEnabled`; if false return
  `{tracking:false}` (no write). Else validate `video_id` (uuid +
  exists), hash IP, insert, return `{session_id}`.
- `Heartbeat`: load active+owner; `409` if not active; compute credited
  + percent; `applyHeartbeat`.
- `Stop`: load (allow already-closed → return as-is); final percent;
  `StopState`; `stop`.
- IP hash: `sha256(salt || clientIP)` truncated to 16 hex chars; salt
  from `MAKTABA_ANALYTICS_SALT` (random per-process default).

## 5. Reaper (`reaper.go`)

```go
type Reaper struct{ DB *sql.DB; StaleTimeout time.Duration; RetentionDays int; Logger *slog.Logger; now func() time.Time }
func (rp *Reaper) RunOnce(ctx) (reaped, purged int64, err error)
func (rp *Reaper) Run(ctx)  // ticker every minute; purge every ~24h of ticks
```

`Run` reaps each minute; a tick counter triggers `purgeOlderThan` once
per 1440 ticks (≈daily). `RetentionDays<=0` skips purge. Retention is
read from `app_settings.analytics.retention_days` at purge time, falling
back to the configured default.

## 6. Tests (`logic_test.go`, `reaper_test.go`)

- `PercentComplete`, `StopState`, `CreditedSeconds` (incl. clamp at
  stale timeout and negative-clock guard), `IsStale` boundary at exactly
  the timeout.
- Lifecycle as a logic sequence: start→heartbeat(+300s)→stop, asserting
  percent/credited transitions without a DB.
- `Reaper.RunOnce` interrupted detection via an injected `sessionRow`
  fixture using a fake clock (a tiny in-memory repo stub satisfying the
  reaper's narrow interface), or — if kept DB-bound — the `IsStale`
  cutoff math.
