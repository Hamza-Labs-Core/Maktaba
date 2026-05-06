# Story 25.16 — Server status dashboard

> Epic 25 · Cloud relay · Phase 4 (operations)

## Description

A "Servers" section in the user's cloud account UI that shows each
linked server: online/offline state, last-seen time, version, public
subdomain, current relay bandwidth this month, software-update
availability, and "Reconnect / Unlink" actions. This is the user's
window into "is my server reachable from the cloud?" and the place
they go when something feels wrong.

Sources of truth:

- Online/offline: `cloud_servers.last_seen_at` updated on every
  tunnel handshake (25.8) and at 30s heartbeat tick. A server is
  "online" if `last_seen_at >= now() - 60s` AND a tunnel handle
  exists in the registry (cross-checked).
- Version: included in the server's `META_ENDPOINTS` frame.
- Subdomain: from `cloud_subdomains` (25.22).
- Bandwidth: this month's sum from `cloud_bandwidth_daily` (25.11).
- Updates available: a backend cron (every 6h) compares
  `cloud_servers.version` to the `latest_stable` value in
  `https://releases.maktaba.app/manifest.json`.

API surface:

- `GET /api/servers` — list of the user's servers with summary.
- `GET /api/servers/{id}` — detail.
- `GET /api/servers/{id}/status` — live status (calls cloud
  registry, not just last-seen DB row).
- `GET /api/servers/{id}/usage?from=...&to=...` — daily bytes.
- `DELETE /api/servers/{id}` — unlinks: closes tunnel, revokes
  bearer token, releases subdomain (with 30-day redirect grace).
- `POST /api/servers/{id}/reconnect-hint` — sends a push to the
  user's *own* devices reminding them to power-cycle the server
  (no direct server control; we never have command channel back).

UI:

- Server card: green dot (online) / amber (recently seen, may be
  reconnecting) / gray (offline >5 min).
- Last seen: relative time ("2 minutes ago"), absolute on hover.
- This-month usage: progress bar against tier cap; turns amber
  at 80%, red at 100%.
- "Update available" badge with link to release notes.
- "Unlink server" with confirm dialog (irreversible warning).
- Empty state: "No servers linked yet — [Add a server]" links
  to claim flow (25.6).

Realtime:

- WS `/ws/servers` (cloud-side; same JWT auth as REST). Events:
  `server.online`, `server.offline`, `server.usage_tick`. Used
  to live-update the card without polling.

## Acceptance criteria

- **Given** a user has 2 linked servers, one online,
  **when** they `GET /api/servers`,
  **then** the response is a 2-element array with correct
  `online: true|false` flags.
- **Given** the user has WS `/ws/servers` open,
  **when** server A's tunnel disconnects,
  **then** within 1s the client receives
  `{type:"server.offline","server_id":"..."}`.
- **Given** a server has not been seen for > 60s,
  **when** the user requests its status,
  **then** `online: false`, `last_seen_at` reflects the
  recorded time.
- **Given** a server's version is older than `latest_stable`,
  **when** the dashboard renders,
  **then** an "Update available" badge with target version
  appears.
- **Given** a user clicks "Unlink",
  **when** they confirm,
  **then** within 5s the tunnel disconnects, the
  `cloud_servers` row is deleted, the subdomain enters 30-day
  grace, and the bearer token row is dropped.
- **Given** an offline server (last_seen 30 min ago),
  **when** the user clicks "Reconnect hint",
  **then** their own devices receive a push notification
  ("Maktaba server X seems offline — please check it").
- **Given** an unlinked server later tries to use its old
  bearer,
  **when** the cloud receives the handshake,
  **then** the response is `401`, the abuse log records the
  attempt, and the server's local UI surfaces "Cloud link
  removed — re-claim to reconnect."

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | server record + tunnel handle | compute online | true |
| T02 | unit        | server record stale 90s | compute | false |
| T03 | integration | tunnel disconnect | observe WS | `server.offline` event within 1s |
| T04 | integration | unlink | observe DB + tunnel | row gone, tunnel closed |
| T05 | regression  | unlinked server tries to reconnect | post handshake | 401, abuse event |
| T06 | unit        | version comparison `0.6.1` < `0.7.0` | compute | update available |
| T07 | integration | usage card | render | progress matches `cloud_bandwidth_daily` sum |
| T08 | regression  | concurrent unlink + tunnel reconnect | observe | unlink wins; reconnect fails |
| T09 | a11y        | screen-reader on offline state | inspect | announces "server X offline, last seen 5 minutes ago" |
| T10 | i18n        | render in Arabic | snapshot | RTL layout valid |

## Edge cases

- **Flapping tunnel.** A server that reconnects every 60s
  may show "online" but trigger frequent state-change events
  (loud WS feed). We debounce client-side: hide
  online↔offline transitions that resolve within 10s.
- **Clock drift.** "Last seen X seconds ago" uses cloud
  clock; client UI calculates relative to its own clock.
  We send `server_last_seen_at` as ISO 8601 UTC; clients
  format relative.
- **Subdomain still claimed after unlink.** Released into
  30-day grace where it 301-redirects to a static "this
  server is no longer available" page. After 30 days
  another user may claim it.
- **Version mistrust.** A compromised server can lie about
  its version. We display server-reported version with a
  low-confidence indicator if it doesn't match a known SHA
  in our release manifest.
- **Multiple servers offline at once.** The "reconnect hint"
  push is rate-limited to 1 / hour / server.
- **Live unlink while playing.** If the user is mid-stream
  on the server they unlink, the in-flight stream gets
  502 within seconds. We surface a confirm-dialog warning.
- **Update available with breaking changes.** Release
  manifest carries a `breaking: bool` per version; the
  badge text becomes "Major update — read notes" when set.
- **Usage chart pagination.** > 90 days of data paginates
  via `next_cursor` (opaque base64 of the tail date).

## Files / packages

- `cloud/internal/server/list.go`,
  `cloud/internal/server/detail.go`,
  `cloud/internal/server/unlink.go`.
- `cloud/internal/server/status_ws.go` — WS endpoint.
- `cloud/internal/jobs/check_releases.go` — release manifest
  cron.
- `web/src/pages/Servers.tsx`,
  `web/src/components/server/ServerCard.tsx`,
  `web/src/components/server/UsageBar.tsx`.

## Open questions

- **Server logs from the cloud.** Should we surface
  server-side error logs in the cloud dashboard? Privacy
  concern — log lines may contain filenames. Defer to a
  v2 opt-in.
- **Auto-update.** Out for v1; releases shipped manually.
