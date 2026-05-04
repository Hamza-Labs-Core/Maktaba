# Plan 10.16 — Security audit log — implementation

> Implementation plan for [story-10-16-security-audit.md](story-10-16-security-audit.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the canonical `audit_log` table is owned by
> Epic 9 Story 9.17 ([README.md](../09-library-management/README.md)) and
> is **not redeclared here** — this story only ships the security-side
> INSERT helpers and the admin-facing read API; the cursor primitive is
> reused from Story 7.2 (cursor encoding).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **No DDL in this plan.** The `audit_log` table, append-only triggers, partitioning, and indexes are owned by Epic 9 Story 9.17. This story inserts rows with `category='security'` and reads them back via `GET /api/security/audit`. | Story header: "Reuses the canonical `audit_log` table"; story AC-2 references Epic 9 Story 9.17 AC-1. | Two stories defining the same table is a recipe for migration drift. The shared table also lets v2 "show me everything user X did" cross-category queries work without a UNION. |
| D2 | **One Go package `internal/audit/security` with one helper per event.** The helpers (`LogLoginSuccess`, `LogLoginFailed`, …) take typed arguments and assemble the `payload_jsonb` blob. Callers pass `audit.Writer` (an interface) so tests can use a fake. The Writer is best-effort. | Story AC-1 event vocabulary. | A typed helper per event makes call sites self-documenting and lets us evolve `payload_jsonb` schema without rewriting every caller. |
| D3 | **Best-effort write: never block the request path.** Helpers enqueue rows on a buffered channel (`cap=1024`). A single goroutine drains the channel and writes via pgx; on failure the row is logged and `audit_write_failed_total{event=…}` is bumped. Channel-full → drop oldest, log `audit_overflow_total`. | Story description: "Best-effort write: never block the request path". | Sync writes on the request path turn an audit-table outage into a service outage. Async + drop-oldest is the standard pattern; we record drops for visibility. |
| D4 | **Sampling for `admin-token.used` in single-user mode**: keep one event per IP per day always, then a 1/min token bucket for the rest. The bucket is per-IP keyed by `cidr/24` to defeat IP-spam. State lives in a small in-memory LRU (`50k` entries). | Story Edge cases. | Single-user mode admin tokens fire on every request, easily 100/s. Naïve writes flood the partition. Sampling preserves the "this IP started using the admin token" event without noise. |
| D5 | **`GET /api/security/audit?cursor=…` reuses the Story 7.2 opaque-cursor primitive.** The cursor encodes `(ts, id)` for stable order even with same-timestamp collisions; page size = 50, max = 200. Non-admin → 403 via `authz.Can(ctx, ActionAuditRead, uuid.Nil)`. | Story AC-3. | Story 7.2 already standardizes opaque cursors with HMAC-signed tamper detection. Re-implementing locally is forbidden by the API style guide. |
| D6 | **Event vocabulary is a Go `const`-list with a CI lint** that rejects new strings except via the curated set in `events.go`. | Story AC-1 enumerates the v1 set. | The vocabulary becomes a contract for analytics dashboards (Epic 22). Open-coded strings drift fast. |
| D7 | **`payload_jsonb` schema is per-event, documented in `events.go`** as a Go struct with `json` tags. The helper marshals the struct via `json.Marshal`. | Story AC-1 mentions event-specific detail. | Strict typing at the call site prevents the `payload_jsonb` from becoming a junk drawer. |

If D3 is rejected (sync writes): a partition lock on the audit table during retention pruning would freeze every authenticated request. We would need to ship a circuit breaker; the async writer is simpler.

---

## 1. Architecture diagram

```
   ┌────────────────────────────────────────────────────────────────┐
   │  hot path — handler / middleware                               │
   │     ▼                                                          │
   │  audit.LogLoginFailed(ctx, audit.LoginFailedPayload{...})      │
   │     │                                                          │
   │     ▼                                                          │
   │  Writer.Enqueue(Row) — non-blocking (D3)                       │
   │     • room in chan → enqueue, return                           │
   │     • full → drop oldest, bump audit_overflow_total,           │
   │              enqueue new (still O(1))                          │
   │  return immediately (no DB I/O on hot path)                    │
   │                                                                │
   │  ─── async drain goroutine ────────────────────────────────────│
   │     for row := range chan:                                     │
   │       if event == "admin-token.used" && !sampler.Allow(ip):    │
   │           continue                                             │
   │       INSERT INTO audit_log (...) VALUES (...)                 │
   │       on err: bump audit_write_failed_total{event}; log         │
   └────────────────────────────────────────────────────────────────┘

   read path:
   ┌────────────────────────────────────────────────────────────────┐
   │  GET /api/security/audit?cursor=...                            │
   │  authz.Can(ctx, ActionAuditRead, uuid.Nil)  -- 403 if not admin│
   │  parse cursor → (after_ts, after_id)                           │
   │  SELECT id, ts, event, actor_user_id, ip, payload_jsonb        │
   │    FROM audit_log                                              │
   │   WHERE category = 'security'                                  │
   │     AND (ts, id) < ($after_ts, $after_id)                      │
   │   ORDER BY ts DESC, id DESC                                    │
   │   LIMIT 50                                                     │
   │  return {items, next_cursor}                                   │
   └────────────────────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout

```
api/
├── internal/
│   └── audit/
│       └── security/
│           ├── events.go          // event consts, payload structs (D6, D7)
│           ├── writer.go          // Writer interface, asyncWriter (D3)
│           ├── helpers.go         // LogLoginSuccess, ... (D2)
│           ├── sampler.go         // admin-token.used sampler (D4)
│           ├── handler.go         // GET /api/security/audit (D5)
│           ├── handler_test.go
│           └── writer_test.go
```

### 2.2 `events.go` — vocabulary + payload types (D6, D7)

```go
// Package security writes Maktaba security-category rows into the shared
// audit_log table (DDL: see ../../../shared/db/migrations/00XX_audit_log.sql,
// owned by Epic 9 Story 9.17). The set of events is closed; new events
// MUST be added here.
package security

// Event is the closed set of `event` values written under category='security'.
type Event string

const (
	EventLoginSuccess          Event = "login.success"
	EventLoginFailed           Event = "login.failed"
	EventLogout                Event = "logout"
	EventLogoutAll             Event = "logout-all"
	EventLockoutUsername       Event = "lockout-username"
	EventLockoutIP             Event = "lockout-ip"
	EventRefreshReplay         Event = "refresh.replay-detected"
	EventPasswordChanged       Event = "password.changed"
	EventKeyRotated            Event = "key.rotated"
	EventAdminTokenUsed        Event = "admin-token.used"
	EventPermissionDenied      Event = "permission.denied"
	EventStreamingDirectAccess Event = "streaming.direct.access"
	EventPairCodeIssued        Event = "pair.code-issued"
	EventPairCodeClaimed       Event = "pair.code-claimed"
	EventPairCodeExpired       Event = "pair.code-expired"
)

// Payload structs — `payload_jsonb` schemas. JSON tags are stable.

type LoginSuccessPayload struct {
	Surface string `json:"surface"` // "web" | "native"
}

type LoginFailedPayload struct {
	UsernameAttempt string `json:"username_attempt"`
	Reason          string `json:"reason"` // "bad_password" | "user_unknown" | "locked"
}

type LogoutPayload struct {
	SessionsRevoked int `json:"sessions_revoked"`
}

type LockoutPayload struct {
	UsernameAttempt string `json:"username_attempt,omitempty"`
	IP              string `json:"ip,omitempty"`
	UntilSec        int64  `json:"until_sec"`
}

type RefreshReplayPayload struct {
	FamilyID string `json:"family_id"`
}

type PasswordChangedPayload struct {
	By string `json:"by"` // "self" | "admin"
}

type KeyRotatedPayload struct {
	OldKid string `json:"old_kid"`
	NewKid string `json:"new_kid"`
}

type AdminTokenPayload struct {
	Reason string `json:"reason,omitempty"` // optional, e.g. "first-use-of-day"
}

type PermissionDeniedPayload struct {
	Action     string `json:"action"`
	ResourceID string `json:"resource_id,omitempty"`
}

type StreamingDirectAccessPayload struct {
	VideoID string `json:"video_id"`
	Path    string `json:"path"`
}

type PairCodePayload struct {
	CodeID    string `json:"code_id"`              // pairing_codes.code_id (NOT the plaintext)
	DeviceK   string `json:"device_kind,omitempty"`
	BundleID  string `json:"bundle_id,omitempty"`
}

type PairCodeExpiredPayload struct {
	Count int `json:"count"`
}
```

### 2.3 `writer.go` — async best-effort writer (D3)

```go
package security

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Row is the in-memory representation of an audit_log INSERT.
type Row struct {
	ID         uuid.UUID
	TS         time.Time
	Event      Event
	ActorUser  *uuid.UUID
	IP         string
	UserAgent  string
	Payload    any // marshalled to payload_jsonb
}

// Writer is the small surface helpers depend on. Test fakes implement it.
type Writer interface {
	Enqueue(r Row)
	Close(ctx context.Context) error
}

const (
	chanCap          = 1024
	insertBatchSize  = 64
	insertFlushEvery = 250 * time.Millisecond
)

type asyncWriter struct {
	pool    *pgxpool.Pool
	log     *slog.Logger
	ch      chan Row
	done    chan struct{}
	sampler *AdminTokenSampler

	overflowCount atomic.Int64
	failedCount   atomic.Int64
}

func NewWriter(pool *pgxpool.Pool, log *slog.Logger) *asyncWriter {
	w := &asyncWriter{
		pool:    pool,
		log:     log,
		ch:      make(chan Row, chanCap),
		done:    make(chan struct{}),
		sampler: NewAdminTokenSampler(50_000),
	}
	go w.run()
	return w
}

func (w *asyncWriter) Enqueue(r Row) {
	if r.Event == EventAdminTokenUsed && !w.sampler.Allow(r.IP, r.TS) {
		return // silently dropped per D4
	}
	select {
	case w.ch <- r:
	default:
		// Full: drop oldest, then enqueue.
		w.overflowCount.Add(1)
		select {
		case <-w.ch: // drop one
		default:
		}
		select {
		case w.ch <- r:
		default:
			// still full (shouldn't happen with single-producer drain) — drop new.
		}
	}
}

func (w *asyncWriter) Close(ctx context.Context) error {
	close(w.ch)
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *asyncWriter) run() {
	defer close(w.done)
	batch := make([]Row, 0, insertBatchSize)
	timer := time.NewTimer(insertFlushEvery)
	defer timer.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.insertBatch(context.Background(), batch); err != nil {
			w.failedCount.Add(int64(len(batch)))
			w.log.Warn("audit.write_failed",
				"category", "security",
				"n", len(batch),
				"err", err.Error())
		}
		batch = batch[:0]
	}
	for {
		select {
		case r, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, r)
			if len(batch) >= insertBatchSize {
				flush()
			}
		case <-timer.C:
			flush()
			timer.Reset(insertFlushEvery)
		}
	}
}

const insertSQL = `
INSERT INTO audit_log
    (id, ts, category, event, actor_user_id, ip, user_agent, payload_jsonb)
VALUES ($1, $2, 'security', $3, $4, $5::inet, $6, $7::jsonb)
`

func (w *asyncWriter) insertBatch(ctx context.Context, rows []Row) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // safe no-op on commit
	for _, r := range rows {
		blob, err := json.Marshal(r.Payload)
		if err != nil {
			blob = []byte(`{}`)
		}
		ip := nullIfEmpty(r.IP)
		_, err = tx.Exec(ctx, insertSQL,
			r.ID, r.TS, r.Event, r.ActorUser, ip, r.UserAgent, blob)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

### 2.4 `helpers.go` — typed call sites (D2)

```go
package security

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// uuidv7 returns a time-ordered UUID. (Imported from internal/uuidv7.)
func uuidv7() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// principal returns the actor for the row, or nil for unauthenticated paths.
type principal interface {
	UserID() uuid.UUID
	HasUser() bool
}

func actorFrom(ctx context.Context) *uuid.UUID {
	// The auth middleware (Story 10.2/10.3) injects a *Principal into ctx.
	if p, ok := ctxPrincipal(ctx); ok && p.HasUser {
		return &p.UserID
	}
	return nil
}

// LogLoginSuccess writes a login.success row.
func LogLoginSuccess(ctx context.Context, w Writer, r *http.Request,
	user uuid.UUID, surface string) {
	w.Enqueue(Row{
		ID: uuidv7(), TS: time.Now().UTC(),
		Event: EventLoginSuccess, ActorUser: &user,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: LoginSuccessPayload{Surface: surface},
	})
}

func LogLoginFailed(ctx context.Context, w Writer, r *http.Request,
	usernameAttempt, reason string) {
	w.Enqueue(Row{
		ID: uuidv7(), TS: time.Now().UTC(),
		Event: EventLoginFailed,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: LoginFailedPayload{UsernameAttempt: usernameAttempt, Reason: reason},
	})
}

func LogLogout(ctx context.Context, w Writer, r *http.Request,
	user uuid.UUID, sessionsRevoked int) {
	w.Enqueue(Row{
		ID: uuidv7(), TS: time.Now().UTC(),
		Event: EventLogout, ActorUser: &user,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: LogoutPayload{SessionsRevoked: sessionsRevoked},
	})
}

func LogLogoutAll(ctx context.Context, w Writer, r *http.Request,
	user uuid.UUID, sessionsRevoked int) {
	w.Enqueue(Row{
		ID: uuidv7(), TS: time.Now().UTC(),
		Event: EventLogoutAll, ActorUser: &user,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: LogoutPayload{SessionsRevoked: sessionsRevoked},
	})
}

func LogPermissionDenied(ctx context.Context, w Writer, r *http.Request,
	action, resourceID string) {
	w.Enqueue(Row{
		ID: uuidv7(), TS: time.Now().UTC(),
		Event: EventPermissionDenied, ActorUser: actorFrom(ctx),
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: PermissionDeniedPayload{Action: action, ResourceID: resourceID},
	})
}

func LogPairCodeIssued(ctx context.Context, w Writer, r *http.Request,
	codeID, deviceKind, bundleID string) {
	w.Enqueue(Row{
		ID: uuidv7(), TS: time.Now().UTC(),
		Event: EventPairCodeIssued,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: PairCodePayload{CodeID: codeID, DeviceK: deviceKind, BundleID: bundleID},
	})
}

func LogPairCodeClaimed(ctx context.Context, w Writer, r *http.Request,
	user uuid.UUID, codeID string) {
	w.Enqueue(Row{
		ID: uuidv7(), TS: time.Now().UTC(),
		Event: EventPairCodeClaimed, ActorUser: &user,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: PairCodePayload{CodeID: codeID},
	})
}

// (Other helpers — LogLockoutUsername, LogLockoutIP, LogRefreshReplay,
// LogPasswordChanged, LogKeyRotated, LogAdminTokenUsed,
// LogStreamingDirectAccess, LogPairCodeExpired — follow the same pattern.)

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP in XFF.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	return r.RemoteAddr
}
```

### 2.5 `sampler.go` — admin-token.used sampler (D4)

```go
package security

import (
	"net"
	"sync"
	"time"
)

// AdminTokenSampler decides whether a given admin-token.used event
// should be persisted. Always-keep:
//   - the first event from a /24 in a given UTC day
// Otherwise:
//   - one event per minute per /24 (token bucket of 1 per minute)
type AdminTokenSampler struct {
	mu      sync.Mutex
	per24   map[string]*sampleState // keyed by /24 prefix
	max     int
}

type sampleState struct {
	lastSeenDay int
	lastEmitted time.Time
}

func NewAdminTokenSampler(max int) *AdminTokenSampler {
	return &AdminTokenSampler{per24: make(map[string]*sampleState, max), max: max}
}

func (s *AdminTokenSampler) Allow(ip string, now time.Time) bool {
	prefix := slashTwentyFour(ip)
	day := now.UTC().YearDay() + now.Year()*1000

	s.mu.Lock()
	defer s.mu.Unlock()

	// Eviction: if at cap, drop a random entry. Cheap; acceptable for this volume.
	if len(s.per24) >= s.max {
		for k := range s.per24 {
			delete(s.per24, k)
			break
		}
	}
	st := s.per24[prefix]
	if st == nil {
		s.per24[prefix] = &sampleState{lastSeenDay: day, lastEmitted: now}
		return true // first-of-day
	}
	if st.lastSeenDay != day {
		st.lastSeenDay = day
		st.lastEmitted = now
		return true // first-of-day for new day
	}
	if now.Sub(st.lastEmitted) >= time.Minute {
		st.lastEmitted = now
		return true
	}
	return false
}

func slashTwentyFour(raw string) string {
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return raw // fallback: bucket by raw string
	}
	if v4 := ip.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}
	// IPv6: collapse to /48.
	v6 := ip.To16()
	for i := 6; i < 16; i++ {
		v6[i] = 0
	}
	return v6.String()
}
```

### 2.6 `handler.go` — admin read API (D5)

```go
package security

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maktaba/api/internal/auth/authz"
	"github.com/maktaba/api/internal/cursor" // Story 7.2 primitive
	"github.com/maktaba/api/internal/httpx"
)

const defaultPageSize = 50
const maxPageSize = 200

type Handler struct {
	pool *pgxpool.Pool
	az   authz.Authz
}

func NewHandler(pool *pgxpool.Pool, az authz.Authz) *Handler {
	return &Handler{pool: pool, az: az}
}

type item struct {
	ID         uuid.UUID       `json:"id"`
	TS         time.Time       `json:"ts"`
	Event      string          `json:"event"`
	ActorUser  *uuid.UUID      `json:"actor_user_id,omitempty"`
	IP         *string         `json:"ip,omitempty"`
	UserAgent  *string         `json:"user_agent,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

type response struct {
	Items      []item  `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// ServeHTTP handles GET /api/security/audit.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h.az.Can(r.Context(), authz.ActionAuditRead, uuid.Nil); err != nil {
		httpx.WriteForbidden(w)
		return
	}
	ps := defaultPageSize
	if v := r.URL.Query().Get("page_size"); v != "" {
		if n, err := parsePosInt(v); err == nil && n > 0 {
			if n > maxPageSize {
				n = maxPageSize
			}
			ps = n
		}
	}
	afterTS, afterID, err := cursor.DecodeAuditCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid-cursor", "invalid cursor")
		return
	}

	const q = `
SELECT id, ts, event, actor_user_id, host(ip) AS ip, user_agent, payload_jsonb
  FROM audit_log
 WHERE category = 'security'
   AND ($1::timestamptz IS NULL OR (ts, id) < ($1, $2))
 ORDER BY ts DESC, id DESC
 LIMIT $3
`
	rows, err := h.pool.Query(r.Context(), q, afterTS, afterID, ps+1)
	if err != nil {
		httpx.WriteProblem(w, http.StatusInternalServerError, "db-error", "audit query failed")
		return
	}
	defer rows.Close()

	out := response{Items: make([]item, 0, ps)}
	var lastTS time.Time
	var lastID uuid.UUID
	count := 0
	for rows.Next() {
		var it item
		var ev string
		if err := rows.Scan(&it.ID, &it.TS, &ev, &it.ActorUser, &it.IP, &it.UserAgent, &it.Payload); err != nil {
			httpx.WriteProblem(w, http.StatusInternalServerError, "db-error", "scan failed")
			return
		}
		it.Event = ev
		count++
		if count > ps {
			next := cursor.EncodeAuditCursor(lastTS, lastID)
			out.NextCursor = &next
			break
		}
		out.Items = append(out.Items, it)
		lastTS, lastID = it.TS, it.ID
	}
	if err := rows.Err(); err != nil {
		httpx.WriteProblem(w, http.StatusInternalServerError, "db-error", "iter failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func parsePosInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadInt
		}
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return 0, errBadInt
		}
	}
	return n, nil
}

var errBadInt = httpError("bad int")

type httpError string

func (e httpError) Error() string { return string(e) }
```

(`cursor.DecodeAuditCursor`/`EncodeAuditCursor` are thin wrappers around the Story 7.2 cursor primitive that pin the field set to `(ts, id)`.)

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `api/internal/audit/security/events.go` | `Event` consts, `*Payload` structs | `TestEventVocabularyClosed` |
| 2 | `api/internal/audit/security/writer.go` | `Writer`, `Row`, `asyncWriter`, `NewWriter` | `TestWriterEnqueueDoesNotBlock`, `TestWriterFlush` |
| 3 | `api/internal/audit/security/sampler.go` | `AdminTokenSampler` | `TestSamplerFirstOfDay`, `TestSamplerOnePerMinute` |
| 4 | `api/internal/audit/security/helpers.go` | `LogLoginSuccess`, …, `clientIP`, `actorFrom` | `TestHelpersBuildExpectedRows` |
| 5 | `api/internal/audit/security/handler.go` | `Handler`, `ServeHTTP` | `TestHandlerForbidsNonAdmin`, `TestHandlerPaginates` |
| 6 | `api/internal/cursor/audit.go` | `EncodeAuditCursor`, `DecodeAuditCursor` (thin wrap of Story 7.2) | `TestAuditCursorRoundTrip` |
| 7 | Lint rule: forbid `event:` string literals outside `events.go` | (CI) | grep-based lint |

---

## 4. Test cases (keyed to ACs)

### AC-1 — Event vocabulary
- `TestEventVocabularyClosed`: assert the public set matches the story's enumeration verbatim (no extras, no omissions).
- Integration: each helper writes a row whose `event` matches the corresponding `Event` constant.

### AC-2 — Append-only
- Inherited from Epic 9 Story 9.17: an UPDATE or DELETE on `audit_log` raises `audit_log is append-only`. We verify with one integration test (`TestAuditLogTriggersBlockUpdateDelete`) that exercises the security-category path.

### AC-3 — Surfaced in API
- Integration: a failed login writes one `category='security', event='login.failed'` row; a successful login writes `event='login.success'` and the writer enqueues exactly once.
- Integration: GET `/api/security/audit` as admin → 200 + items newest-first.
- Integration: GET `/api/security/audit` as non-admin → 403 problem+json `type=forbidden` (uses the Plan 10.13 helper).
- Integration: pagination — issue 120 events, fetch page 1 (50), follow `next_cursor`, fetch page 2, compare contents. No duplicates, no gaps.
- `TestHandlerPaginates`: handler-level cursor round-trip.

### AC-4 — Retention via partitioning
- Inherited from Epic 9 Story 9.17. We add a smoke test `TestRetentionDoesNotBreakSecurityReads` that detaches a one-month-old partition while the read API is exercised: rows in newer partitions still surface.

### AC-1 (sampler edge — Edge cases)
- `TestSamplerFirstOfDay`: first event from a /24 always allowed, even at high rate.
- `TestSamplerOnePerMinute`: 100 events in 60s from same /24 → 1 + 1 = 2 emitted (first-of-day, then one minute-bucket).
- Integration: under single-user mode admin-token use of 100/s for 5 seconds, the writer enqueues ≤ 6 rows (1 first-of-day + 5 minute buckets).

### Helpers
- `TestHelpersBuildExpectedRows`: each helper's `Row` shape matches the corresponding `Payload` schema; `payload_jsonb` JSON round-trips.
- `TestWriterEnqueueDoesNotBlock`: with the channel full, 1000 enqueues complete in ≤ 5 ms and `audit_overflow_total` advances.

---

## 5. Edge cases

| #   | Case | Handled by |
|-----|------|------------|
| E1  | Audit DB outage during a login flood. The async writer's INSERT fails; rows accumulate in the in-memory channel; eventually overflow triggers drop-oldest. The login handler is unaffected. Once DB recovers, the writer drains. | D3. |
| E2  | A login.success and a logout fire microseconds apart. Both rows have UUIDv7 IDs; the read API orders by `(ts, id) DESC` so the cursor breaks ties stably. | D5 + UUIDv7. |
| E3  | An IP appears in `X-Forwarded-For` as `192.0.2.1, 10.0.0.5`. `clientIP` returns the leftmost client IP. We trust the proxy header because Caddy is the only ingress (Plan 10.15). | helpers.go. |
| E4  | A unicode username in `username_attempt`. JSON marshal handles it; `payload_jsonb` is `text` under the hood and stores UTF-8 fine. | json.Marshal. |
| E5  | A racing partition swap (the maintenance job DETACHes the partition that contains rows the writer is about to insert into). The INSERT goes into the parent table; pg_partman / our maintenance keeps the next partition pre-created. The write rate window is well below partition swap risk. | Documented in operations runbook. |
| E6  | Cursor includes future timestamps (clock skew between API replicas — single-host deployments make this moot). The `(ts, id) < ...` comparison treats future cursor as "no rows older" → empty result. | D5. |
| E7  | A helper called with a nil principal but `actor_user_id` field defined. `actorFrom(ctx)` returns `nil`; the `payload_jsonb.payload.actor_user_id` is omitted. | helpers.go. |
| E8  | Sampler memory growth in single-user mode behind CGNAT (many distinct /24s). LRU cap (50k) keeps memory bounded; eviction is "drop one random entry"; replays from an evicted /24 trigger a fresh first-of-day. | sampler.go. |
| E9  | `audit_overflow_total` and `audit_write_failed_total` are exposed via `/metrics`; ops alerts fire when either is non-zero for > 5 min. | Documented in operations notes. |
| E10 | Reading the audit feed during a partition retention prune. `pg_partman` issues `ALTER TABLE … DETACH PARTITION CONCURRENTLY` (Postgres 16) so reads continue on other partitions. Our handler issues a single SELECT on the parent table. | Postgres 16 capability. |
| E11 | A pair-code event written before the row is committed (helper called inside a still-open xact). The async writer uses its own pool connection, so the event lands regardless of the caller's xact outcome. We document that helpers are post-commit best-effort and may emit events for transactions that ultimately rolled back — preferable to losing events. | helpers.go (documented). |

---

## 6. Acceptance checklist

- [ ] **A1** Closed `Event` set in `events.go` exactly matches the story AC-1 enumeration; each event has a typed `Payload` struct.
- [ ] **A2** Async best-effort `Writer` enqueues without blocking; on full channel drops oldest and bumps `audit_overflow_total`; on insert failure bumps `audit_write_failed_total`.
- [ ] **A3** `LogLoginSuccess`/`LogLoginFailed`/`LogLogout`/etc. helpers build the correct `Row`, populate `actor_user_id` from ctx where appropriate, fetch IP via `X-Forwarded-For`-aware `clientIP`.
- [ ] **A4** `AdminTokenSampler` keeps first-of-day per /24 and 1/min thereafter; bounded memory.
- [ ] **A5** `GET /api/security/audit` returns admin-only, newest-first, paginated via the Story 7.2 opaque cursor; non-admin gets the 403 problem+json from Plan 10.13.
- [ ] **A6** Append-only enforcement is inherited from Epic 9 Story 9.17 triggers; no DDL is added in this plan.
- [ ] **A7** Integration: a failing INSERT does not fail the request; metric advances; logs include `category=security` and the event name.
- [ ] **A8** Lint rule blocks new `event:` string literals outside `events.go`.

---

## 7. Cross-references

- `audit_log` DDL: see [../09-library-management/README.md](../09-library-management/README.md) §"`audit_log` (canonical audit table)" and Story 9.17.
- Cursor primitive: Story 7.2 (`internal/cursor`), reused by every paginated list endpoint.
- `authz.ActionAuditRead`: defined in [plan-10-13-permission-model.md](plan-10-13-permission-model.md), enforces 403 for non-admin reads.
- `payload.code_id` for pair events: written by [plan-10-17-auth-pair.md](plan-10-17-auth-pair.md), never the plaintext code.
