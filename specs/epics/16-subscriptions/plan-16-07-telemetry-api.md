# Implementation Plan — Story 16.7 API: telemetry sink

> Companion to [story-16-07-telemetry-api.md](story-16-07-telemetry-api.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `shared/db/migrations/0064_telemetry.sql` (Postgres + SQLite variant). |
| sqlc queries | `shared/db/queries/telemetry.sql`. |
| Allow-list source | `api/internal/telemetry/allowlist.go` — declared in code (single source of truth, versioned with binary). |
| HTTP handlers | `api/internal/http/telemetry.go` mounted at `/api/telemetry` and `/api/telemetry/web-vitals`. |
| Redaction | `api/internal/telemetry/redact.go` — strips library root paths; called server-side as defense-in-depth (client also strips, [Story 16.5](story-16-05-telemetry-opt-in.md)). |
| Retention sweep | `api/internal/telemetry/retention.go` — daily cron; deletes per-table policy. |
| Server-side opt-out | `[telemetry] enabled = false` in config; endpoints return 204 no-write. |
| Out of scope | Client opt-in flow ([Story 16.5](story-16-05-telemetry-opt-in.md)); analytics dashboards ([Epic 21](../21-observability/) — out of this epic). |

## 1. Architecture diagram

```
   Client (Story 16.5) ──► POST /api/telemetry            ──► allow-list filter
                           POST /api/telemetry/web-vitals      ▼
                                                            redact paths
                                                                ▼
                                                          INSERT batched
                                                                │
                                                                ▼
                                                  Postgres: telemetry_events
                                                            telemetry_web_vitals
                                                                │
                                                                ▼
                                                      retention sweeper (daily)
```

## 2. Database migration

`shared/db/migrations/0064_telemetry.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE telemetry_events (
    id              BIGSERIAL PRIMARY KEY,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    device_pseudonym TEXT NOT NULL,
    event_kind      TEXT NOT NULL,
    app_version     TEXT,
    os              TEXT,
    os_version      TEXT,
    locale          TEXT,
    payload         JSONB NOT NULL
);
CREATE INDEX telemetry_events_received_at_idx
    ON telemetry_events (received_at);
CREATE INDEX telemetry_events_kind_received_idx
    ON telemetry_events (event_kind, received_at);
CREATE INDEX telemetry_events_pseudonym_idx
    ON telemetry_events (device_pseudonym);

CREATE TABLE telemetry_web_vitals (
    id              BIGSERIAL PRIMARY KEY,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    device_pseudonym TEXT NOT NULL,
    -- FID was deprecated in Web Vitals v3 in 2024 (replaced by INP).
    -- We accept it from legacy clients (≤ web-vitals 2.x bundle) but
    -- new client builds emit INP only; the analytics dashboard charts
    -- INP and folds historical FID rows into the same series for
    -- continuity. Drop FID from the CHECK once telemetry from
    -- pre-INP clients ages out (≥ 90 days after the rollout).
    metric          TEXT NOT NULL CHECK (metric IN ('LCP','FID','CLS','INP','TTFB')),
    value           DOUBLE PRECISION NOT NULL,
    route           TEXT
);
CREATE INDEX telemetry_web_vitals_received_at_idx
    ON telemetry_web_vitals (received_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS telemetry_web_vitals;
DROP TABLE IF EXISTS telemetry_events;
-- +goose StatementEnd
```

## 3. Allow-list

`api/internal/telemetry/allowlist.go`:

```go
package telemetry

type AllowedKind struct {
    Kind   string
    Fields []string  // payload field names
}

var Allowed = []AllowedKind{
    {"app.open",          []string{"cold_start", "route"}},
    {"search.run",        []string{"result_count", "has_filters", "duration_ms"}},
    {"search.suggest",    []string{"suggestion_count"}},
    {"player.start",      []string{"source", "codec", "is_hdr"}},
    {"player.error",      []string{"error_kind", "duration_ms"}},
    {"library.scan_done", []string{"duration_ms", "added", "removed"}},
    {"transcribe.done",   []string{"duration_ms", "model"}},
    {"error.uncaught",    []string{"error_message", "stack_first_5"}},
    // ... extend in future PRs; never silently extend at runtime
}

var allowedSet = func() map[string]map[string]struct{} {
    m := map[string]map[string]struct{}{}
    for _, a := range Allowed {
        f := map[string]struct{}{}
        for _, x := range a.Fields { f[x] = struct{}{} }
        m[a.Kind] = f
    }
    return m
}()

func IsKindAllowed(kind string) bool { _, ok := allowedSet[kind]; return ok }

func StripUnknownFields(kind string, payload map[string]any) map[string]any {
    f, ok := allowedSet[kind]
    if !ok { return nil }
    out := map[string]any{}
    for k, v := range payload {
        if _, ok := f[k]; ok { out[k] = v }
    }
    return out
}
```

## 4. Redaction

`api/internal/telemetry/redact.go`:

```go
type Redactor struct {
    libraryRoots []*regexp.Regexp
}

func NewRedactor(roots []string) *Redactor {
    rs := make([]*regexp.Regexp, len(roots))
    for i, r := range roots {
        rs[i] = regexp.MustCompile(regexp.QuoteMeta(r))   // EC: regex metachars in paths
    }
    return &Redactor{rs}
}

func (r *Redactor) Apply(s string) string {
    for _, re := range r.libraryRoots {
        s = re.ReplaceAllString(s, "<library>")
    }
    return s
}
```

The library roots are loaded from `libraries.toml` at startup; on `library.added` / `library.deleted` events (Epic 9 plan-09-15 — these are the canonical channel names; earlier drafts of this plan used `library_added`/`library_removed` which do not match the publisher), the redactor is rebuilt.

The story EC pins: "A library root path containing regex metacharacters: the redaction filter `regexp_quote`s before substituting." `regexp.QuoteMeta` is the Go equivalent.

## 5. HTTP handlers

`api/internal/http/telemetry.go`:

```go
func MountTelemetry(r chi.Router, s *telemetry.Service, cfg Config) {
    r.Route("/telemetry", func(r chi.Router) {
        r.Use(rateLimitPerIP(1000, time.Minute))   // 1k events/min/IP
        r.Post("/", postEvents(s, cfg))
        r.Post("/web-vitals", postWebVitals(s, cfg))
        r.Delete("/devices/{pseudonym}", deleteDevice(s))
    })
}

func postEvents(s *telemetry.Service, cfg Config) http.HandlerFunc {
    type event struct {
        EventKind   string         `json:"event_kind"`
        Payload     map[string]any `json:"payload"`
        Ts          time.Time      `json:"ts"`
        AppVersion  string         `json:"app_version"`
        OS          string         `json:"os"`
        OSVersion   string         `json:"os_version"`
        Locale      string         `json:"locale"`
        Pseudonym   string         `json:"device_pseudonym"`
    }
    type body struct { Events []event `json:"events"` }
    return func(w http.ResponseWriter, r *http.Request) {
        if !cfg.Enabled {
            w.WriteHeader(204); return         // server-side kill switch
        }
        var b body
        if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&b); err != nil {
            problem(w, 400, "invalid-json", ""); return
        }
        if len(b.Events) > 100 {
            problem(w, 413, "too-many-events", "max=100"); return
        }
        for _, e := range b.Events {
            if !telemetry.IsKindAllowed(e.EventKind) {
                problem(w, 400, "unknown-event-kind", e.EventKind); return
            }
        }
        // Atomic batch insert; either all 100 or zero.
        rows := make([]telemetry.Event, 0, len(b.Events))
        for _, e := range b.Events {
            payload := telemetry.StripUnknownFields(e.EventKind, e.Payload)
            payload = redactPayload(s.Redactor, payload)
            rows = append(rows, telemetry.Event{
                Pseudonym: e.Pseudonym, Kind: e.EventKind,
                AppVersion: e.AppVersion, OS: e.OS, OSVersion: e.OSVersion, Locale: e.Locale,
                Payload: payload,
            })
        }
        // The all-or-nothing guarantee promised by the AC ("either
        // all 100 or zero") requires a transaction; `InsertEvents`
        // wraps a single `BEGIN; INSERT...; COMMIT` so a midway error
        // rolls back the whole batch. The interface is:
        //
        //   func (s *Service) InsertEvents(ctx context.Context, rows []Event) error
        //   // Implementation: BEGIN, prepared INSERT loop, COMMIT;
        //   // any non-nil error from the loop triggers ROLLBACK.
        //
        // The TestPostEventsAtomic case injects a deliberate row
        // failure on row N>0 and asserts zero rows persist.
        if err := s.InsertEvents(r.Context(), rows); err != nil {
            problem(w, 500, "internal", ""); return
        }
        w.WriteHeader(204)
    }
}

func deleteDevice(s *telemetry.Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        pseudonym := chi.URLParam(r, "pseudonym")
        // Public endpoint: pseudonym is a self-asserted bearer.
        // Rate-limited at the chi middleware (1/IP/sec) to prevent enumeration.
        if err := s.DeleteByPseudonym(r.Context(), pseudonym); err != nil {
            problem(w, 500, "internal", ""); return
        }
        w.WriteHeader(204)
    }
}
```

## 6. IP scrubbing

The story AC: "IP addresses: dropped at the API edge; never persisted."

Implementation: the chi middleware emits the IP for rate-limit bookkeeping only; the handler does **not** read `r.RemoteAddr` into the row. Rate-limit state (token bucket) is in-memory and indexed by IP, but expires after a minute and never persists.

To make this auditable, we add a static check: a CI test greps `telemetry_events` insert paths for `RemoteAddr` and fails if found.

## 7. Retention sweep

```go
// api/internal/telemetry/retention.go
func (r *Retention) Run(ctx context.Context) {
    t := time.NewTicker(24 * time.Hour); defer t.Stop()
    for {
        select {
        case <-t.C: r.tick(ctx)
        case <-ctx.Done(): return
        }
    }
}

func (r *Retention) tick(ctx context.Context) {
    _, _ = r.db.Exec(ctx, `DELETE FROM telemetry_events
                           WHERE received_at < now() - interval '90 days'`)
    _, _ = r.db.Exec(ctx, `DELETE FROM telemetry_web_vitals
                           WHERE received_at < now() - interval '30 days'`)
}
```

## 8. Test plan

### 8.1 Allow-list

| Test | What it pins |
|---|---|
| `TestUnknownKindRejected` | `event_kind = "private.evil"` → 400 unknown-event-kind. |
| `TestUnknownFieldsStripped` | `payload.{is_hdr, secret}` for `player.start` (only `is_hdr` allowed) → stored row has no `secret`. |
| `TestEmptyAllowedFieldsNoFields` | A kind with no allowed fields stores an empty payload `{}`. |

### 8.2 Redaction

| Test | What it pins |
|---|---|
| `TestRedactsLibraryRoot` | `error_message = "/Users/me/Lectures/foo.mp4"` with library root `/Users/me/Lectures` → stored row's message is `<library>/foo.mp4`. |
| `TestRedactsRootWithRegexMeta` | Library root `/data[archive]/` (with brackets) → quoted-meta replacement works. |
| `TestNoLeakageOfNonRoot` | Other paths unaffected. |

### 8.3 HTTP

| Test | What it pins |
|---|---|
| `TestPostEventsBatchUpTo100` | 100 events → 204; 101 events → 413. |
| `TestPostEventsAtomic` | One bad kind in a batch → 400; zero rows inserted. |
| `TestPostEventsServerDisabled` | `[telemetry] enabled = false` → 204; no rows. |
| `TestPostWebVitalsValidates` | Metric not in {LCP,FID,CLS,INP,TTFB} → 400. |
| `TestDeleteByPseudonymPurges` | Insert N rows; DELETE → SELECT returns 0. |
| `TestDeleteRateLimited` | 100 rapid DELETEs → eventually 429. |
| `TestRateLimitPerIP` | 1001 events/min from one IP → 429 on the 1001st. |

### 8.4 Retention

| Test | What it pins |
|---|---|
| `TestRetentionDeletesEvents91DaysOld` | Insert with `received_at = now() - 91d`; tick → row gone. |
| `TestRetentionDeletesVitals31DaysOld` | Same for web-vitals. |

### 8.5 IP

| Test | What it pins |
|---|---|
| `TestNoRemoteAddrInTelemetryEvents` | Runtime test: post 5 events from a known IP via the test server; query `telemetry_events.payload` for any field containing the test IP — assert zero matches. (Earlier draft used a source-grep which produced false positives whenever `req.RemoteAddr` appeared in unrelated middleware.) |
| `TestRateLimitStateInMemoryOnly` | After server restart, no rate-limit residue. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| `event.ts` 24 h in the future | Truncate to `received_at`; warn (log only). | `TestFutureTimestampTruncated` |
| 1,001 events in one request | 413; nothing inserted. | `TestPostEventsBatchUpTo100` |
| Library root with regex metacharacters | `QuoteMeta` quotes; redaction works. | `TestRedactsRootWithRegexMeta` |
| Telemetry endpoint exposed to internet | Rate-limited 1k/min/IP; server-side disable available. | `TestRateLimitPerIP` + config |
| Pseudonym enumeration via DELETE | Pseudonym is 96 random bits; brute force infeasible; rate-limit caps attempts. | `TestDeleteRateLimited` |
| Migration on SQLite | `BIGSERIAL` becomes `INTEGER PRIMARY KEY`; we test SQLite variant explicitly. | `TestMigrationSQLite` |
| Server clock skew on retention | Uses `now()`; assumed server clock authoritative. | n/a |
| `payload` larger than 16 KiB | Soft cap at 16 KiB; oversize → reject the event with 413. | `TestPayloadOversizeRejected` |
| Allow-list change in a release | Old client sends a kind no longer in allow-list → 400; client logs and drops. | `TestAllowlistEvolution` |

## 10. Acceptance checklist

**Schema**
- [ ] `telemetry_events` and `telemetry_web_vitals` exist on Postgres + SQLite.

**API**
- [ ] Endpoints accept allow-listed kinds; strip unknown fields.
- [ ] Server-side `enabled = false` returns 204 with no write.
- [ ] DELETE by pseudonym purges atomically.

**Redaction**
- [ ] Library roots stripped from string fields; regex-meta safe.

**Retention**
- [ ] Daily cron; 90 d / 30 d.

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.7.
