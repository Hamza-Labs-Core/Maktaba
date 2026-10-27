# Story 16.7 — API: usage data to Maktaba Cloud

The transport and stores of [Story 16.5](story-16-05-telemetry-opt-in.md)'s anonymous usage
data: the home's report, the pipe apps send through, the home's outbox and sender, and
Maktaba Cloud's ingest, store and operator view.

## Rework (2026-10-26)

The first version (slot 0156) was a sink on the home: public `POST /api/telemetry` and
`/api/telemetry/web-vitals` storing events in `telemetry_events` / `telemetry_web_vitals`
for 90 / 30 days, read by no one but SQL. The data now goes to Maktaba Cloud (story 16.5
says why); slot **0161** deletes what both tables held (they stay, empty, for the previous release; a
later slot drops them) and adds `telemetry_outbox`.

## AC

### The home

- `POST /api/telemetry/app` (any signed-in user; who posted is never kept): an app's batch
  `{consent, install_id, app, platform, form_factor, app_version, os_version, locale,
  events: [{kind, payload, ts}], vitals: [{metric, value, route}]}`. Forwarded only with
  `consent: true` (else 204, nothing kept), whatever the admin's server switch says.
  At most 100 events and 50 vitals (413); unknown kind or metric 400; per-IP budget 1,000
  a minute (429). Each field is normalized to what the cloud takes; free text is scrubbed
  of library roots, home folders, other paths (the file name too; its extension stays),
  origins, query strings, emails, IP addresses and host names; a route becomes its
  template.
- `DELETE /api/telemetry/app/{install_id}` (signed in): drops the install's waiting
  batches and queues a `forget` for the cloud. 204 whether or not anything existed.
- `GET /api/telemetry/server` (admin): `{sharing, enabled, pending, oldest_pending,
  last_sent_at, last_error, preview}`; the preview is the day's report as it would go.
- `POST /api/telemetry/server/forget` (admin): drops the server's waiting reports, queues a
  `forget` under the home's id, and gives the home a new id.
- The admin's switch is `telemetry.share_server_data` (`PATCH /api/settings`, admin only).
- The sender (a goroutine of the home's API) adds the day's report once a UTC day while
  the switch is on (off, it drops the server's waiting reports and the error counts), and
  delivers the outbox to `{cloud}/v1/telemetry` in envelopes of at most 20 batches and
  480 KiB: 2xx clears them; 429, 5xx or no answer puts them off (1 min, doubled, at most
  6 h, or Retry-After); another 4xx sends each batch of the envelope alone and drops the
  refused one. A batch a week old is dropped unsent; at most 5,000 wait.
- The cloud is the license server's (the cloud the home is claimed in, else
  `MAKTABA_LICENSE_SERVER`, else `https://api.maktaba.app`); `MAKTABA_TELEMETRY_URL`
  overrides (`off`: nowhere). `[telemetry] enabled = "off"`: nothing is queued or sent.
- Server errors are counted in memory by kind (every Error-level log record's message and
  `event` attribute, never its other attributes) and drained by each report.

### Maktaba Cloud

- `POST /v1/telemetry` (anonymous): `{schema: 1, batches: [server | app | forget]}`.
  At most 512 KiB (413) and 20 batches; one invalid batch refuses the envelope (400);
  per-IP and per-install rate limits (429 + Retry-After); a second scrubbing pass; one
  transaction. No address is stored or logged.
- Storage: server reports (one per install and day, a resend replaces it), app installs,
  app events, error groups and occurrences, Web Vitals. Retention: reports 400 days, app
  data and error occurrences 90, vitals 30.
- `GET /v1/admin/usage?days=N` (service admins): installs, versions, platforms, install
  kinds, apps in use, feature use, error groups, vitals p75. Shown in the cloud console's
  Admin → Usage.

## TC

- Switch off: the sender sends nothing of the home's; an app batch with consent still goes.
- Switch on: one server report a day reaches the cloud.
- An app batch without consent: 204 and nothing queued.
- A viewer: 403 on the server's routes and on the switch; signed out: 401 everywhere.
- An error message with a library path, a file name and an address: none of them leaves
  the home.
- The cloud down: the batches wait and are retried; a week later they are gone.

## Implementation

**Status: done (reworked 2026-10-26).**

- Home: `api/internal/handlers/telemetry` (`report.go`, `errcount.go`, `outbox.go`,
  `sender.go`, `telemetry.go`, `allowlist.go`, `redact.go`, `ranges.go`), wired by
  `api/internal/router/telemetry.go` and `api/main.go`; slot 0161
  `shared/db/migrations/0161_usage_data_outbox.sql` (+ `.sqlite.sql`); the routes are off
  the auth gate's public allowlist.
- Cloud: `cloud/internal/telemetry`, `cloud/internal/handlers/admin/usage.go`, migration
  `cloud/migrations/00150001_usage_telemetry.sql`, `web/cloud/src/pages/admin/Usage.tsx`,
  `cloud/docs/usage-data.md`.
- Tests: the home's `api/internal/handlers/telemetry/*_test.go` (the pipe and its roles,
  scrubbing, the sender against a fake cloud, Postgres), `settings` (the switch's role);
  the cloud's `cloud/internal/telemetry` and `cmd/maktaba-cloud` integration tests.
