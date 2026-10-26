# Anonymous usage data (Epic 16 story 16.7)

Homes (self-hosted Maktaba servers) can share anonymous usage data with
Maktaba Cloud: a daily **server report** when the home's admin turned on
`telemetry.share_server_data`, and **app batches** from each app whose
user opted in on that device (the home forwards them; apps never talk to
the cloud). Neither consent implies the other. A home with
`[telemetry] enabled = "off"` in `server.toml` sends nothing at all.

Code: `internal/telemetry` (validation, scrubbing, store, retention,
aggregates), `internal/handlers/admin/usage.go` (operator view),
migration `00150001_usage_telemetry.sql`.

## What is anonymous about it

- Every row is keyed by an **install id**: 16–32 random letters/digits
  the home or the app makes at opt-in (an app retires it when its user
  turns sharing off). No table has a foreign key to accounts, servers or
  users, and no column holds an address.
- The client address is read **only** for the rate limit (in memory) and
  is never stored. The access log leaves `remote_ip` out for
  `/v1/telemetry` (`middleware.NoIPLogPaths`).
- Counts are reported as ranges (`0`, `1`, `2-5`, `6-20`, `21-100`,
  `101-1k`, `1k-10k`, `10k-100k`, `100k+`), never exact numbers.
- Free text (error messages, stack frames) is scrubbed by the sender and
  again by the cloud (`telemetry.Scrub`): URLs, e-mail addresses, home
  folders (`/Users/x`, `/home/x`, `C:\Users\x`), the directories of
  absolute paths (the whole path unless its file is source code, so a
  stack frame keeps `main.go:12`), IPv4/IPv6 addresses, host names and
  query strings are removed.

## Ingest — `POST /v1/telemetry`

Public: no account, no server secret.

```json
{ "schema": 1, "batches": [
  { "type": "server", "install_id": "…", "day": "2026-10-26", "report": { … } },
  { "type": "app", "install_id": "…", "app": "web", "platform": "web", "app_version": "1.2.3", "events": [ … ], "vitals": [ … ] },
  { "type": "forget", "install_id": "…" }
]}
```

The exact shapes are in the contract
(`usage-data-cloud.md`, "Exact shapes"). The cloud validates strictly:

| Field | Rule |
|---|---|
| `install_id` | `[A-Za-z0-9]{16,32}` |
| `app`, `platform`, `form_factor` (may be empty), `os`, `arch`, `install_kind` | token `[a-z0-9._-]{1,32}` |
| `version`, `app_version`, `os_version` | `[0-9A-Za-z][0-9A-Za-z.+_-]{0,39}` |
| `locale` | BCP-47-ish, ≤ 20 chars, optional |
| counts in a report | one of the ranges |
| `day` | `YYYY-MM-DD`, from 7 days ago to tomorrow (UTC) |
| report `apps` / `features` / `errors` | ≤ 50 each; feature names `[a-z0-9_]{1,40}`, values bool or a range; error `count` 0–1e9 |
| app `events` / `vitals` | ≤ 100 / ≤ 50 |
| event kinds | `app.open{cold_start}`, `feature.used{feature,count}`, `player.start{source,codec}`, `player.error{error_kind,duration_ms}`, `error.uncaught{error_message,stack_first_5}`; another kind is a 400 |
| payload | scalar values only (an object or array anywhere is a 400); fields not listed are dropped; listed fields are type-checked |
| event `ts` | RFC 3339; clamped into [now − 30 days, now] |
| vitals | metric `LCP`/`INP`/`CLS`/`FCP`/`TTFB`/`FID`, value 0–600000, route like `/videos/:id` (query/fragment cut) |
| free text | scrubbed, then cut to 300 chars (message) / 200 (first frame) |

Responses:

| Status | When |
|---|---|
| 204 | stored (the whole envelope, in one transaction) |
| 400 | any invalid batch — the envelope is rejected as a whole |
| 413 | body over 512 KiB, or more than 20 batches |
| 429 + `Retry-After` | rate limit: per address 120 burst, 2/s; per install id 30 burst, 1 per 20 s. `forget` is never limited by install id |

Server reports upsert on (install id, day): a resend of the day replaces
the report and that day's server errors. App batches add rows.
`error.uncaught` events go to the error tables, not the events table.
`forget` deletes every row of the install id (and error groups left
empty).

## Storage and retention

| Table | Holds | Kept |
|---|---|---|
| `telemetry_server_reports` | one report per home per day | 400 days |
| `telemetry_app_installs` | per app install per day: app, platform, form factor, versions, locale | 90 days |
| `telemetry_app_events` | allow-listed events | 90 days |
| `telemetry_error_groups` / `telemetry_error_occurrences` | groups (fingerprint = hash of source, app, scrubbed message with digits folded, first frame) and counts per install, day and version | occurrences 90 days; a group goes with its last occurrence |
| `telemetry_web_vitals` | web-vitals samples | 30 days |

The api role runs the sweep at start and then daily
(`telemetry.RunSweeper`, `DefaultRetention`).

## Operator view — `GET /v1/admin/usage?days=30`

Service admins only (the same gate as every `/v1/admin/*` route; anyone
else gets 403 `admin_access_denied`). `days` is 1–400 (default 30). It
returns: homes and app installs active in the window (and per app);
server versions (homes, from each home's latest report in the window);
app versions per app; server OS/arch and install kinds; app platforms and
form factors; apps in use (homes whose reports list the app with any
people or requests, and opted-in installs that sent batches); feature
share of homes; `feature.used` totals per app; the top 50 error groups
(source, app, message, first frame, count, installs, versions, first and
last seen); and web-vitals p75 per metric.

The cloud console shows it at **Admin → Usage** (`/admin/usage`) with a
7/30/90/400-day selector.
