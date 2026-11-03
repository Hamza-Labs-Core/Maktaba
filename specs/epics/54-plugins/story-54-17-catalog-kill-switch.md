# Story 54.17 — The home's catalog, updates and the kill switch

> Epic 54 · Phase 2 · Depends on: 54.07, 54.14, 54.15, 54.37, Epic 51 (51.05) · Needed by: 54.18

## Why

The home server, not any plugin, talks to the store: it fetches the signed catalog, downloads
packages, offers updates, and above all honours the revocation list so a plugin Maktaba
withdraws for malware stops in every home within minutes. This is the server reaching the
internet on its own behalf, straight to the store's CDN (`plugins-cdn.maktaba.tv`, 54.15), with
signatures, not the channel, deciding what is trusted. Nothing here goes through Maktaba Cloud
or needs the home to be linked to it, so a cloud outage changes nothing for plugins.

## Description

### 1. Fetching

A new API loop, `api:plugins_catalog`, declared on the API's Epic 51 loop runner
(`runner.Declare`, 51.05) and listed in the `loops` of the `plugins.host` component (54.02), so
it runs while `plugins.host` is enabled. Inside, it fetches only while the home has a plugin
installed or an admin opened Browse in the last 7 days:

| What | When | How |
|---|---|---|
| `revoked-head.json` | every 5 minutes (± 30 s jitter) | HTTPS from `plugins-cdn.maktaba.tv`; a tiny signed `{version, issued, sha256}`, cached at the edge for 60 s; a version newer than the home's triggers a `revoked.json` fetch |
| `revoked.json` | every hour, and when the head says it changed | same |
| `store-keys.json` | with every catalog fetch and every `revoked.json` fetch | same |
| `catalog-<api major>.json` | every 6 hours while plugins are installed; on opening Browse if older than 1 hour | same |
| packages | on install or update | CDN by sha256 (paid ones through a signed URL, 54.19); verified before use |

All the lists are verified (54.14) and kept in `<data>/state/plugins/` and Postgres with the
newest-seen versions. Requests for public files carry nothing identifying (a plain GET with the
server version in the `User-Agent`). The only store requests a home signs are those of a home
linked to a store account (54.15 §5): `seen` (skipped with telemetry off), rating eligibility
and entitlements. The 5-minute head poll replaces a push: it reaches every home, linked or
not, costs Cloudflare cache hits only, and needs no connection held open to any Maktaba
server.

### 2. Updates

- For each installed plugin, the newest catalog version that matches the server version,
  plugin API and platform is "available". The Plugins page shows Update; the admin chooses.
- **Auto-update** per plugin, off by default; when on, updates that add no capability apply at
  night (the server's update window), through 54.07's side-by-side update. Updates that add
  capabilities always wait for the admin.
- A store install is refused while the revocation list is expired (54.14 §4).

### 3. The kill switch

On every new revocation list, the home matches entries against installed plugins (id, version
range, developer key id) and acts:

| Action | Home does |
|---|---|
| `disable` | `plugin_installs.revoked = true`, which closes the `revoked` gate (54.37 §3): every runner sees *Waiting* `revoked` (drain ≤ 30 s, then force-pause; queue kept), and the API also sets an Epic 51 hold (`hold_by = "store-revocation"`, audit `component.hold`). Admins get a notice with the reason code's words; the plugin cannot be started again (`409 plugin_revoked` on `/start` and on `PUT /api/components/plugin.<id>` with `want = enabled`); it can only be removed. Its writes stay, and the Plugins page offers "Remove and clear what it wrote". |
| `warn` | a warning on the plugin and a notice; it keeps running; the admin may disable it |
| `delist` | nothing changes for installed copies; no updates offered |

`disable` also applies to unreviewed copies with a matching id and developer key. Revocation
works for file-installed store plugins as soon as the home sees a list.

The store's `disable` is the malware switch-off and cannot be undone by an admin. It is
separate from the admin's own **keep off** (54.07, decided 2026-11-03): an admin's
disable sticks through updates, restores and reinstalls until the admin turns it on; turning
it on never lifts a `disable` revocation. Auto-update never turns on a kept-off plugin.

### 4. Offline homes

A home that never reaches the store never gets a fresh list; each server release carries the
list as of its build (54.14 §4), applied on update. The Plugins page always shows when the list
was last updated.

### 5. Roles

`GET /api/plugins/catalog`, `POST /api/plugins/install {store: {id, version}}`,
`PUT /api/plugins/{id}/auto-update`: admin only, role-tested. Viewers never trigger a store
fetch.

## Acceptance criteria

- **Given** a home (linked to a store account or not, linked to the cloud or not) with plugin
  P 1.2 installed,
  **when** the store adds `disable` for P `<1.3`,
  **then** within 7 minutes (5-minute poll, 60 s edge cache, jitter) P is held with `revoked`,
  running jobs drained, admins notified, and Start answers `409 plugin_revoked` (N10).
- **Given** Maktaba Cloud is unreachable,
  **when** the same entry is published,
  **then** P is held in the same time; no request goes to any cloud host.
- **Given** a `revoked-head.json` naming a version the fetched `revoked.json` does not match,
  **when** fetched,
  **then** the home keeps its current list, retries on the next poll and logs
  `revocation_head_mismatch`.
- **Given** a `warn` entry,
  **when** applied,
  **then** P keeps running and the warning shows.
- **Given** the home was served a revocation list older than one it has,
  **when** it fetches,
  **then** it keeps the newer one and logs `revocation_rollback_refused`.
- **Given** auto-update on and a new version that adds a capability,
  **when** the night window comes,
  **then** it does not apply; the admin sees "Needs your approval".
- **Given** a viewer,
  **when** it calls `GET /api/plugins/catalog`,
  **then** 403 and no fetch happens.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, go) | fake CDN; head poll | publish disable | held ≤ 7 min (fake clock) |
| T02 | integration (PG, go) | fake CDN; cloud host blackholed; no store link | publish | held ≤ 7 min; zero requests to cloud hosts |
| T03 | integration (PG, go) | warn / delist | apply | per table |
| T04 | unit (go) | rollback, expiry | fetch | per 54.14 rules |
| T05 | integration (PG, go) | auto-update with/without new capability | night window | applied / waits |
| T06 | role test (go) | catalog, install-from-store, auto-update routes | 4 roles | 401/403/403/200 |
| T07 | integration (go) | package with bad sha from CDN | install | refused; deleted |
| T08 | CI update rehearsal | release with embedded list containing a disable for an installed test plugin | update | plugin held after update |

## Edge cases

- **A revocation for a version range that includes the previous (rollback) version**: rollback
  to it is refused too.
- **Revoked plugin's queued jobs**: kept (so a mistaken revocation, later lifted, loses
  nothing); removed with the plugin.
- **A revocation lifted** (entry removed in a newer list): the plugin stays held; the admin may
  start it again; a notice says it was cleared.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/plugins/catalog/` (new) | Loop, fetch, verify, updates, kill switch. |
| `api/internal/handlers/plugins/catalog.go` (new) | Routes. |
| `shared/components/catalog.toml` | `api:plugins_catalog` in the `loops` of `plugins.host`. |
| `web/src/i18n/{en,ar}.json` | Revocation reasons, notices. |
| `tests/update_rehearsal/` | T08 scenario. |

### Tests

- Integration T01–T03, T05, T07; unit T04; role T06; CI T08 (update rehearsal; touches the
  updater path, so it gates the tag).

### Rollout

Dark until the store lists plugins. The hourly revocation fetch runs only while a plugin is
installed.

### Rollback

Release rollback; held plugins stay held (desired state in the database).

### Dependencies

54.07, 54.14, 54.15.
