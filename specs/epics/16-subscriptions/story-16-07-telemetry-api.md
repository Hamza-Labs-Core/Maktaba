# Story 16.7 — API: telemetry sink

**Status:** **NEW** — added in response to
[REVIEW §3.2](../../REVIEW.md): Story 16.5 and Epic 21 Story 21.2 EC-3
referenced `POST /api/telemetry` and `POST /api/telemetry/web-vitals`
without an owning story. This story owns both endpoints, the storage
schema, the redaction filter, and the on-server retention policy.

## AC

### Schema

- New table `telemetry_events`:
  - `id BIGSERIAL PRIMARY KEY`
  - `received_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `device_pseudonym TEXT NOT NULL` (16 random chars; client-generated
    once at opt-in time, never linked to user)
  - `event_kind TEXT NOT NULL`
    (e.g., `app.open`, `search.run`, `player.start`, `error.uncaught`)
  - `app_version TEXT`
  - `os TEXT`, `os_version TEXT`
  - `locale TEXT`
  - `payload JSONB NOT NULL` (sanitized; see redaction below)
- New table `telemetry_web_vitals`:
  - `id BIGSERIAL PRIMARY KEY`
  - `received_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `device_pseudonym TEXT NOT NULL`
  - `metric TEXT NOT NULL` (`LCP`, `FID`, `CLS`, `INP`, `TTFB`)
  - `value DOUBLE PRECISION NOT NULL`
  - `route TEXT` (route template, e.g., `/watch/:id`, NEVER the real id)
- Indexes: `(received_at)` for retention sweep; `(event_kind,
  received_at)` for analyst queries.
- Migration owner: this story.

### Endpoints

- `POST /api/telemetry {events: [{event_kind, payload, ts}]}` →
  `204` accept up to 100 events per request; over → `413`.
- `POST /api/telemetry/web-vitals {metrics: [{metric, value, route, ts}]}` →
  `204` accept up to 50 metrics per request.
- `DELETE /api/telemetry/devices/{device_pseudonym}` → `204` purges all
  rows for that pseudonym (the "Forget my device" affordance from
  Story 16.5). Public — anyone with the pseudonym can purge their own
  data; the pseudonym acts as a self-asserted bearer.
- All POST endpoints accept anonymous traffic but rate-limit per IP
  (1k events/min) to prevent flooding.

### Redaction (ENFORCED at the API boundary)

- Server-side allow-list of `event_kind` values; any unknown kind →
  `400 unknown-event-kind`.
- For each known kind, an allow-list of `payload` field names; unknown
  fields are stripped silently.
- IP addresses: dropped at the API edge; never persisted (Story 21.8
  is the canonical source for the privacy policy).
- Free-text fields (`error_message` for `error.uncaught`) are scanned
  for paths matching the configured library roots and stripped; the
  filter is a `regexp_replace` over each `library.root_path` known to
  the server.

### Self-host & retention

- `[telemetry] enabled = false` (config) disables ingestion: endpoints
  return `204` but never write rows. Ensures the spec's "self-host
  server-side opt-out" promise is tested.
- Retention: 90 days for `telemetry_events`, 30 days for
  `telemetry_web_vitals`. A nightly sweep removes older rows.

## TC

- Client posts a sanitized `app.open` event: row appears in
  `telemetry_events`.
- Client posts an `error.uncaught` event whose `error_message` contains
  `/Users/me/Lectures/foo.mp4` (a known library root): the persisted
  row has `…/foo.mp4` stripped to `<path>/foo.mp4` (root only;
  filename retained because it's not personally identifying).
- Client posts an `event_kind = "private.evil"` (not in allow-list):
  400 with `unknown-event-kind`.
- DELETE the device pseudonym: subsequent SELECTs return zero rows.
- `[telemetry] enabled = false` and a POST: 204 but the table is still
  empty.

## EC

- Client time skew (events arrive with a `ts` 24 h in the future):
  truncate to `received_at` for ordering; log warning.
- Network burst (1,001 events in a request): the 1,001st returns 413;
  the first 1,000 are not persisted (atomic).
- A library root path containing regex metacharacters: the redaction
  filter `regexp_quote`s before substituting.
- Telemetry endpoint reachable from the open internet: rate limit
  prevents abuse; admins can disable entirely via the config flag.

## Implementation

**Status: done.** Built on branch `worktree-agent-a80d9c8f289bb66b9`.

- **Schema:** slot **0156** `shared/db/migrations/0156_telemetry.sql` (+ `.sqlite.sql`):
  `telemetry_events`, `telemetry_web_vitals`, indexes on `received_at`,
  `(event_kind, received_at)` and the pseudonym (for "Forget my device"). Not in a
  machine's copy: slot 0155 lists both tables in `cluster_copy_excluded()`.
- **Handlers:** `api/internal/handlers/telemetry` (allow-list, redaction, handlers,
  retention sweeper), mounted by `api/internal/router/telemetry.go`; the three routes
  are on the auth gate's public allowlist (`DefaultPublicAllowlist`, pinned by its test).
- **Rate limit:** the existing token-bucket limiter (`api/internal/middleware/ratelimit.go`)
  gains `IPBudget`, which takes n tokens at once: 1,000 events + metrics a minute per IP
  (counted per event, not per request), 30 deletes a minute per IP; 429 with `Retry-After`.
  The IP is never stored (`TestNoRemoteAddrInTelemetry` checks every column).
- **Config:** `[telemetry] enabled` in server.toml (default on) reaches the API as
  `MAKTABA_TELEMETRY_ENABLED=false` when off; the POSTs then answer 204 and store
  nothing. "Forget my device" works either way.
- **Retention:** `telemetry.Sweeper`, started in `api/main.go` beside the watch reaper:
  a pass at start, then daily; events 90 days, Web Vitals 30 days.
- **Tests:** `telemetry_test.go` (SQLite, from the migration file) covers the plan's §8
  allow-list, redaction, HTTP, retention and IP tests; `telemetry_pg_test.go` runs the sink
  on Postgres with every migration applied (`MAKTABA_TEST_PG_URL`).

**Differences from the spec and plan (the repo's conventions win):**

- The handlers live in `api/internal/handlers/telemetry`, not `api/internal/telemetry` +
  `api/internal/http`; no sqlc (the API uses `database/sql`).
- Slot 0156, not 0064. Postgres indexes are `CONCURRENTLY` under `NO TRANSACTION`
  (migration-lint).
- The request body carries the device context once: `{device_pseudonym, app_version, os,
  os_version, locale, events: [{event_kind, payload, ts}]}` (web vitals:
  `{device_pseudonym, metrics: [...]}`).
- An extra nullable column `occurred_at` keeps the device's `ts`; a time more than a
  minute ahead of the server is truncated to `received_at` and logged (the EC).
- The library-root placeholder is `<library>` (the plan), not `<path>` (the story's TC).
  Home folders (`/Users/x`, `/home/x`, `C:\Users\x`) are also replaced, with `<home>`. The
  roots are read from `library_roots` (cached one minute) rather than rebuilt on
  `library.added` events.
- Payload values must be scalars (an object or list is dropped like an unknown field);
  strings are capped at 2,048 characters; a payload over 16 KiB is 413.
- Allow-list additions: `feature.used {feature, count}` (story 16.5's feature-usage counts)
  and `app.open.library_size_bucket`.
- A 4xx-refused batch is never retried by the client; the per-request caps answer 413
  `too-many-events` with nothing stored (the transaction makes a batch all or nothing).
- No admin read endpoint (the plan asks for none). The rows are read with SQL.
