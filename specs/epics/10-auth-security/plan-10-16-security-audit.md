# Implementation Plan — Story 10.16 Audit log for security-sensitive actions

> Companion to [story-10-16-security-audit.md](story-10-16-security-audit.md).
> Reuses the canonical `audit_log` table owned by Epic 9 Story 9.17 (the
> append-only triggers and monthly partitioning are *not* re-implemented
> here). This story owns the *security event vocabulary* and the
> security-only read endpoint.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Table | `audit_log` (Epic 9 Story 9.17). This plan **does not** alter the schema; it pins the row shape that security writers must use. |
| Sink interface | `api/internal/auth/audit.go` — `AuditSink.Record(ctx, AuditEvent)`. Concrete impl writes to `audit_log` via sqlc. |
| Event types | Strongly-typed Go structs implementing `AuditEvent`; each carries `Category()`, `Event()`, and `Payload()`. |
| Read endpoint | `GET /api/security/audit?cursor=…` — admin-only; cursor pagination newest-first. |
| Sampling | Story 10.9's `admin-token.used` is sampled in the middleware before reaching the sink. The sink itself does not sample. |
| Idempotency / dedupe | Lockout audits (Story 10.11) may double-write under racing failures; the sink dedupes on `(category, event, target, minute_bucket)` via `ON CONFLICT DO NOTHING` on a partial unique index — see §3. |
| Out of scope | Audit table partitioning + retention (Story 9.17). The append-only triggers (also 9.17). Non-security categories. |

## 1. Architecture diagram

```
                ┌──────────────────────────────────────┐
                │ AuditEvent (typed structs)            │
                │  - AuditLoginFailed                   │
                │  - AuditLoginSuccess                  │
                │  - AuditLogout / AuditLogoutAll       │
                │  - AuditLockoutUsername / IP          │
                │  - AuditRefreshReplay                 │
                │  - AuditPasswordChanged               │
                │  - AuditKeyRotated                    │
                │  - AuditAdminTokenUsed                │
                │  - AuditPermissionDenied              │
                │  - AuditStreamingDirectAccess         │
                │  - AuditPairCodeIssued / Claimed      │
                │  - AuditAdminRevoke                   │
                │  - AuditRateLimited                   │
                └─────────────────┬────────────────────┘
                                  │ Record(ctx, ev)
                                  ▼
                ┌──────────────────────────────────────┐
                │ AuditSink (audit.go)                  │
                │   sink.Record(ctx, ev):                │
                │     row := build(ev, ctxUser, now)     │
                │     INSERT INTO audit_log              │
                │       (category, event, payload, ...)  │
                │       ON CONFLICT (dedupe key)         │
                │       DO NOTHING                       │
                └─────────────────┬────────────────────┘
                                  │
                                  ▼
                ┌──────────────────────────────────────┐
                │ audit_log (Story 9.17 schema)         │
                │   id, category, event, actor_user_id, │
                │   subject, payload_jsonb, ip,         │
                │   created_at (partitioned)            │
                └──────────────────────────────────────┘

GET /api/security/audit?cursor=<base64(created_at|id)>
   ▼
┌───────────────────────────────────────────────────────┐
│ http/security_audit.go                                 │
│   - requireAdmin                                       │
│   - SELECT ... WHERE category='security'               │
│        AND (created_at, id) < cursor ORDER BY DESC    │
│        LIMIT 50                                        │
│   - response: {entries: [...], next_cursor: ...}      │
└───────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/auth/audit.go` | `AuditSink` interface; concrete impl. |
| `api/internal/auth/audit_events.go` | All event structs implementing `AuditEvent`. |
| `api/internal/http/security_audit.go` | `GET /api/security/audit`. |
| `shared/db/queries/audit_security.sql` | sqlc queries for inserts and the read endpoint. |
| `api/internal/auth/audit_test.go` | Unit tests for the sink. |
| `api/internal/http/security_audit_test.go` | Read-endpoint tests. |
| `shared/db/migrations/0026_audit_security_dedupe.sql` | Partial unique index for dedupe. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/http/router.go` | Mount `/api/security/audit` (admin-only). |
| All security-emitting paths (Stories 10.2–10.17) | Replace ad-hoc `audit.Record` calls with the typed event structs. |

### 2.3 Type definitions

```go
// api/internal/auth/audit.go
package auth

import (
    "context"
    "encoding/json"
    "net"
    "time"

    "github.com/google/uuid"
)

type AuditEvent interface {
    Category() string             // always "security" in this file
    Event() string                // event vocabulary, see below
    Subject() string              // human-readable subject (username, ip, key id, etc.)
    Payload() map[string]any
    DedupeKey() string            // empty = no dedupe; otherwise (event|subject|minute)
    IP() net.IP                   // optional; nil OK
}

type AuditSink interface {
    Record(ctx context.Context, ev AuditEvent)
}
```

```go
// api/internal/auth/audit_events.go (excerpt)
type AuditLoginSuccess struct {
    UserID   uuid.UUID
    Username string
    Surface  string   // "web" | "native"
    IPAddr   net.IP
}
func (e AuditLoginSuccess) Category() string { return "security" }
func (e AuditLoginSuccess) Event()    string { return "login.success" }
func (e AuditLoginSuccess) Subject()  string { return e.Username }
func (e AuditLoginSuccess) Payload() map[string]any {
    return map[string]any{"user_id": e.UserID, "surface": e.Surface}
}
func (e AuditLoginSuccess) DedupeKey() string { return "" }
func (e AuditLoginSuccess) IP() net.IP { return e.IPAddr }

type AuditLockoutUsername struct {
    UserID   uuid.UUID
    Username string
    Count    int
    Window   time.Duration
    IPAddr   net.IP
}
func (e AuditLockoutUsername) Event()    string { return "lockout-username" }
func (e AuditLockoutUsername) DedupeKey() string {
    return fmt.Sprintf("lockout-username|%s|%d", e.Username,
        time.Now().Truncate(time.Minute).Unix())
}
// ... and so on for every event in §3.
```

### 2.4 Event vocabulary

Per AC-1, `event ∈`:

| Event | Subject | Notable payload |
|---|---|---|
| `login.success` | username | `user_id, surface` |
| `login.failed` | username | `surface, reason: invalid|unknown` |
| `logout` | username | `surface, session_id?` |
| `logout-all` | username | `web_revoked, refresh_revoked` |
| `lockout-username` | username | `count, window, ip` (DEDUPED per minute) |
| `lockout-ip` | ip | `count, window` (DEDUPED per minute) |
| `refresh.replay-detected` | user_id | `family_id, ip, revoked_count, reason?` |
| `password.changed` | user_id | `by_admin?, target_user_id` |
| `key.rotated` | kid | `mode: overlap|immediate` |
| `admin-token.used` | ip | `path?` (sampled before reaching sink) |
| `permission.denied` | user_id | `action, resource_id` (sampled — see §6) |
| `streaming.direct.access` | video_id | `user_id, ip` |
| `pair.code-issued` | code_hash | `device_kind, ip` |
| `pair.code-claimed` | code_hash | `user_id, ip` |
| `auth.rate-limited` | ip | `scope, path` (DEDUPED per minute per (scope, ip)) |

## 3. Dedupe migration

`shared/db/migrations/0026_audit_security_dedupe.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- A partial unique index on the audit_log rows that have a dedupe_key
-- in their payload. `dedupe_key` lives in payload_jsonb so we don't
-- alter the canonical schema; the index uses an expression to extract
-- it. Rows without a dedupe_key are not constrained.

ALTER TABLE audit_log
  ADD COLUMN IF NOT EXISTS dedupe_key TEXT GENERATED ALWAYS AS
    (payload_jsonb ->> 'dedupe_key') STORED;

-- Postgres rule: a unique index on a partitioned table MUST include
-- every partition key column. `audit_log` is monthly-partitioned by
-- `created_at` (Story 9.17), so the unique constraint is on
-- (created_at, dedupe_key). The dedupe semantics still hold per
-- minute-bucket because the bucket is encoded into `dedupe_key` itself
-- (see §2.3 — `time.Now().Truncate(time.Minute).Unix()`); the
-- `created_at` column just satisfies the partition-key requirement.
CREATE UNIQUE INDEX IF NOT EXISTS audit_log_security_dedupe
    ON audit_log (created_at, dedupe_key)
    WHERE category = 'security' AND dedupe_key IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_security_dedupe;
ALTER TABLE audit_log DROP COLUMN IF EXISTS dedupe_key;
-- +goose StatementEnd
```

The sink writes `dedupe_key` into `payload_jsonb` (and the generated
column extracts it) so the existing audit schema and triggers from
9.17 don't need changes.

The SQLite variant uses an `INDEX ... WHERE` partial unique index on
the same JSON expression (`json_extract(payload_jsonb, '$.dedupe_key')`).

## 4. Sink implementation

```go
// api/internal/auth/audit.go
type sink struct {
    db *db.Queries
}

func NewSink(db *db.Queries) AuditSink { return &sink{db: db} }

func (s *sink) Record(ctx context.Context, ev AuditEvent) {
    actor := uuid.Nil
    if u, ok := UserFromContext(ctx); ok {
        actor = u.ID
    } else if AdminTokenPathFromContext(ctx) {
        actor = SentinelUserID
    }
    payload := ev.Payload()
    if payload == nil { payload = map[string]any{} }
    if dk := ev.DedupeKey(); dk != "" {
        payload["dedupe_key"] = dk
    }
    payloadJSON, _ := json.Marshal(payload)

    err := s.db.InsertAuditLog(ctx, db.InsertAuditLogParams{
        Category:    ev.Category(),
        Event:       ev.Event(),
        ActorUserID: nullableUUID(actor),
        Subject:     ev.Subject(),
        IP:          ev.IP().String(),
        Payload:     payloadJSON,
    })
    // ON CONFLICT DO NOTHING is in the SQL; this swallows the dedupe
    // case as a no-op. Other errors get a WARN and the sink continues.
    if err != nil {
        slog.Warn("audit sink insert failed", "event", ev.Event(), "err", err)
    }
}
```

## 5. SQL

`shared/db/queries/audit_security.sql`:

```sql
-- name: InsertAuditLog :exec
-- The ON CONFLICT target matches the partial unique index defined in
-- migration 0026 (see §3): (created_at, dedupe_key) WHERE category =
-- 'security' AND dedupe_key IS NOT NULL. The created_at column is
-- required because audit_log is partitioned by created_at (monthly,
-- Story 9.17); Postgres requires every partition key column in any
-- unique index on a partitioned table.
INSERT INTO audit_log (category, event, actor_user_id, subject, ip, payload_jsonb)
VALUES ($1, $2, $3, $4, $5::inet, $6::jsonb)
ON CONFLICT (created_at, dedupe_key) WHERE category = 'security' AND dedupe_key IS NOT NULL
DO NOTHING;

-- name: ListSecurityAudit :many
SELECT id, category, event, actor_user_id, subject, ip, payload_jsonb, created_at
  FROM audit_log
 WHERE category = 'security'
   AND (created_at, id) < (
        COALESCE($1::timestamptz, 'infinity'::timestamptz),
        COALESCE($2::bigint,       9223372036854775807)
   )
 ORDER BY created_at DESC, id DESC
 LIMIT 50;
```

## 6. Sampling

Two events are noisy and deserve sampling at the *emitter*, not the
sink. Sampling at the sink would lose context information needed for
the dedupe key.

| Event | Sampling rule | Where |
|---|---|---|
| `admin-token.used` | First per (ip, day) always; afterwards 1/min/IP via token bucket. | Story 10.9 middleware (already in plan-10-09 §5). |
| `permission.denied` | Sample at 1/min per `(user_id, action)`; first per minute logged. | `api/internal/http/middleware/audit_perm_denied.go` (new helper, lives in this story). |

The dedupe index in §3 is the *defense in depth*; the emitter-level
sampling is the *primary cost control*. Lockout / rate-limit events
use the dedupe index alone (they're naturally bursty and we want one
row per per-minute bucket).

## 7. Read endpoint

```go
// api/internal/http/security_audit.go
func listSecurityAudit(q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cur := decodeCursor(r.URL.Query().Get("cursor"))
        rows, err := q.ListSecurityAudit(r.Context(), cur.CreatedAt, cur.ID)
        if err != nil {
            problem(w, http.StatusInternalServerError, "internal", "")
            return
        }
        var nextCursor string
        if len(rows) == 50 {
            last := rows[len(rows)-1]
            nextCursor = encodeCursor(last.CreatedAt, last.ID)
        }
        writeJSON(w, http.StatusOK, map[string]any{
            "entries":     rows,
            "next_cursor": nextCursor,
        })
    }
}
```

The cursor is `base64url(json{created_at, id})`. Mounted with
`requireAdmin`; non-admin → 403 (story AC-3).

## 8. Test plan

### 8.1 Sink (`audit_test.go`)

| Test | What it pins |
|---|---|
| `TestSinkInsertsRow` | Record(LoginSuccess) → row exists with `category='security'`, `event='login.success'`, payload JSON contains `user_id` and `surface`. |
| `TestSinkAttributesActorFromCtx` | ctx has user u; Record any event → `actor_user_id == u.ID`. |
| `TestSinkUsesSentinelOnAdminTokenPath` | ctx flagged as admin-token path → `actor_user_id == SentinelUserID`. |
| `TestSinkDedupeIndex` | Two consecutive `lockout-username` writes for the same user in the same minute → only ONE row. |
| `TestSinkAcrossMinuteBoundary` | Same event in two different minutes (clock advanced) → two rows. |
| `TestSinkInsertFailureDoesNotPanic` | DB returns ErrConnLost → WARN logged, Record returns without raising. |
| `TestEventVocabularyExhaustive` | A table-driven test that constructs every event type and asserts `Event()` matches the expected vocabulary. |

### 8.2 Endpoint (`security_audit_test.go`)

| Test | What it pins |
|---|---|
| `TestListReturnsRowsNewestFirst` | Insert 5 events at staggered times; GET → entries in descending `created_at`. |
| `TestListPagination` | Insert 60 events; GET → 50 entries + non-empty `next_cursor`; GET with cursor → next 10 + empty cursor. |
| `TestListNonAdminReturns403` | Non-admin GET → 403. |
| `TestListUnauthenticatedReturns401` | Anonymous GET → 401. |
| `TestListEntriesIncludePayload` | The response payload includes `event`, `subject`, `actor_user_id`, `payload_jsonb`. |
| `TestListExcludesNonSecurityCategories` | Insert one `category='library'` audit row + one `category='security'`; GET → only security row. |

### 8.3 End-to-end coverage

| Test | What it pins |
|---|---|
| `TestLoginFailedWritesAudit` | Wrong password → one `event='login.failed'` row. |
| `TestLoginSuccessResetsLockoutCounter` | Login success → one `event='login.success'` row + the user's `failed_attempts` reset (cross-story integration). |
| `TestRefreshReplayWritesAuditOnce` | Replay attack → one `event='refresh.replay-detected'` row with `revoked_count`. |
| `TestKeyRotateWritesAudit` | `keys rotate` → one `event='key.rotated'` row. |
| `TestPairFlowWritesIssueAndClaim` | Full pair flow → two rows: `pair.code-issued` then `pair.code-claimed`. |
| `TestAdminTokenSampledOncePerMinute` | 60 admin-token requests in 30s from one IP → exactly 1 audit row (first); after the bucket replenishes, more rows appear. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| High-volume admin-token use | Sampler at 1/min per IP plus first-per-day. | Story 10.9 `TestMiddlewareEmitsAuditOncePerMinute` |
| `audit.Record` called from a request with no user (e.g., login.failed for unknown user) | `actor_user_id` is NULL; `subject` carries the attempted username; `ip` carries the request IP. | `TestSinkAttributesActorFromCtx` (variant) |
| Audit insert fails mid-handler | Logged WARN; the security path continues (security guarantee already enforced before the audit write). | `TestSinkInsertFailureDoesNotPanic` |
| Read endpoint pagination across partition boundaries | Story 9.17's monthly partitions are read transparently via the parent table. | n/a |
| Two replicas race the same lockout audit insert | Dedupe index makes one INSERT a no-op; both code paths return without error (ON CONFLICT DO NOTHING). | `TestSinkDedupeIndex` |
| Event payload contains a secret-like value (e.g., a stack trace with a token) | The sink does not auto-redact JSON payloads; emitters must not put secrets in payloads. Documented; CONTRIBUTING checklist for new event types. | docs |
| Cursor decoded from a tampered request | A bad base64 → 400 `bad-cursor`; preserves the API's own input validation contract. | `TestListBadCursorReturns400` |
| Audit table partitioned by month | Story 9.17 owns; this story doesn't change. | n/a |
| Read endpoint returns extremely old rows | Bounded by the 50-row LIMIT and cursor; the operator can scroll back as far as retention allows. | n/a |

## 10. Dependencies

No new dependencies. The dedupe index is a `STORED` generated column
in PG and a partial unique index over `json_extract` in SQLite.

## 11. Acceptance checklist

**Vocabulary**
- [ ] AC-1: every event in the vocabulary table is implementable as an `AuditEvent` struct; `TestEventVocabularyExhaustive` passes.

**Append-only**
- [ ] AC-2: relies on Story 9.17's BEFORE UPDATE/DELETE triggers; no UPDATE/DELETE path in this story.

**Read endpoint**
- [ ] AC-3: `GET /api/security/audit` admin-only; non-admin → 403; cursor pagination newest-first.

**Retention**
- [ ] AC-4: relies on Story 9.17's monthly partitioning + retention sweep; no per-category override.

**Dedupe**
- [ ] Lockout / rate-limit / sampled events deduped by per-minute key.

**Sampling**
- [ ] `admin-token.used` sampled at 1/min/IP with first-per-day always.
- [ ] `permission.denied` sampled at 1/min per (user, action).

**Tests**
- [ ] All §8 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.16.
- [ ] CONTRIBUTING note: new security events must define an `AuditEvent` struct and never include plaintext secrets in `Payload()`.
