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
