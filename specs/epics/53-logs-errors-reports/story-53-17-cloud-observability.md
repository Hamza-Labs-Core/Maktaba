# Story 53.17 — The cloud on the same model, feeding Epic 50

> Epic 53 · Phase 4 · Depends on: 53.01, 53.02, 53.10 · Needed by: 53.16, Epic 50.10

## Why

The cloud (`cloud/cmd/maktaba-cloud`: API, relay, worker; `maktaba-cloudlink` on homes) logs plain
JSON to stdout at Info, outside the shared logger, with no redaction contract and nowhere to
search. Epic 50 runs it on two nodes and a witness, and its monitoring (50.10), incidents (50.12)
and postmortems need the cloud's logs, errors and application metrics. This story makes the cloud
a part like any other, and is the **single source** of those for Epic 50 (README §9).

## Description

### 1. The same logger and schema

`maktaba-cloud` and `maktaba-cloudlink` use `shared/log/go` (53.01): `service` = `cloud.api`,
`cloud.relay`, `cloud.worker`, `cloudlink`; `node` = `A`, `B`, `W` (or the host name off the HA
setup); `home_h` instead of server ids and slugs; `request_id` passed through the relay (53.08).
Never logged: account emails, Stripe ids beyond their prefix, resume tickets (50.03 says so),
tokens.

### 2. Logs on each node, and off it

- Each node runs the **log keeper** as a library (`shared/logkeeper/go`, extracted from 53.02's
  `cmd/maktaba-server/internal/logkeeper`): the cloud processes' output (via the systemd unit's
  `StandardOutput=` to a pipe the keeper reads, or the container runtime's log driver into it)
  → `/var/lib/maktaba-cloud/logs/segments/…`, the same files, retention **14 days** (Epic 50 §4.5),
  cap 5 GB per node.
- **Forwarding:** each node's keeper streams its segments (`warn`+ at once, everything else
  batched every 10 s) over the Hetzner private network to the witness W (Epic 50.07), which keeps
  a copy for 14 days. A node that dies leaves its last lines up to the moment of death (≤ 10 s
  for `info`, ≤ 1 s for `warn`+) on W. Before 50.07 exists, forwarding is off and the node's own
  copy is all there is.
- **The index** (53.09's role, as `maktaba-cloud logs`) runs on W over all nodes' copies, so the
  service admin can search the whole cloud from one place even while A or B is down.

### 3. Errors

The cloud's own `error`/`fatal` lines are grouped with 53.10's fingerprint into the index on W.
The cloud admin's **Errors** view (`app.maktaba.tv/admin/errors`, service admin only) lists, side
by side: the cloud's own groups (from W's index) and the anonymous home/app groups
(`telemetry_error_groups`, existing), with report counts from 53.16.

### 4. Application metrics for Epic 50.10

The cloud's existing `/metrics` (architecture §13.12) gains, with the names and labels fixed here
(`shared/metrics/cloud-series.md`):

| Metric | Labels | Meaning |
|---|---|---|
| `maktaba_errors_total` | `service`, `code` | error lines, by error code (the counter 50.10's `cloud_error_spike` alert uses) |
| `maktaba_http_requests_total`, `maktaba_http_request_seconds` | `service`, `route` (pattern), `status_class` | API and relay request rate and latency |
| `maktaba_log_lines_total`, `maktaba_log_dropped_total`, `maktaba_log_forward_lag_seconds` | `service`, `level` | the log path's own health |
| `maktaba_problem_reports_total` | `result` | reports received (53.16) |

Epic 50.10 scrapes them, adds them to its Grafana dashboards, and owns the alert rules
(including `cloud_error_spike`: `maktaba_errors_total` rate over 5× its 7-day baseline for 10
minutes; and `log_forward_lagging`: lag > 60 s for 5 minutes). This story only emits and names
them. **Home** metrics never go to the cloud's Prometheus (the home keeps its own, 53.12).

### 5. Runbooks and the rehearsal

- Every Epic 50.12 runbook's **Check** step links to a saved search in the cloud log viewer
  (e.g. "relay errors last 30 min", "failover window") instead of SSH and `journalctl`.
- Epic 50.06's cloud rehearsal adds: after `failover-kill-leader`, the killed node's last `warn`+
  lines are on W and searchable.

## Acceptance criteria

- **Given** the relay logs a failed request,
  **then** the line has `service: cloud.relay`, `node`, `request_id`, `home_h`, and no slug, email or token.
- **Given** node A is killed (rehearsal),
  **when** the service admin searches the 2 minutes before,
  **then** A's last `warn`+ lines (≤ 1 s before the kill) are found on W.
- **Given** a cloud error happens 100 times,
  **then** the cloud admin's Errors view shows one group with count 100, next to the anonymous home groups.
- **Given** Prometheus on W scrapes the API,
  **then** `maktaba_errors_total` and the request metrics are there with the labels above and no per-home label.
- **Given** a home admin's cloud token,
  **then** the cloud log and error views answer 403.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (cloud) | handlers logging | lines | 53.01 schema; scrub cases pass |
| T02 | unit | keeper forwarder with a fake W | network drop and back | no gap, no duplicate |
| T03 | unit | `/metrics` | scrape | names and labels as §4; no `server_id`/slug labels |
| T04 | role test (cloud) | home admin, service admin | log, error routes | 403, 200 |
| T05 | **CI only** (Epic 50.06) | `failover-kill-leader` | search on W | last lines found |
| T06 | unit | `promtool test rules` (in 50.10's files) | synthetic `maktaba_errors_total` | `cloud_error_spike` fires at the threshold |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/cmd/maktaba-cloud/main.go`, `cloud/cmd/maktaba-cloudlink/main.go`, `cloud/internal/**` (loggers) | §1. |
| `shared/logkeeper/go/` (new, extracted from 53.02) | Keeper as a library, forwarder. |
| `cloud/cmd/maktaba-cloud/logs.go` (new) | The `logs` subcommand on W. |
| `cloud/internal/metrics/` | §4 metrics. |
| `shared/metrics/cloud-series.md` (new) | Names and labels. |
| `cloud/internal/handlers/admin/logs.go`, `errors.go` (new); `web/cloud/src/pages/admin/Logs.tsx`, `Errors.tsx` (new) | Service admin views. |
| `deploy/cloud-ha/` (Epic 50) | Keeper on each node, index on W, the systemd units. |

### Tests

T01–T04 in the cloud's unit job; T05 in Epic 50.06's rehearsal; T06 in 50.10's `promtool` tests.

### Rollout

Logger first (a cloud release); the keeper and forwarding with Epic 50.07's nodes; the admin views
after.

### Rollback

Revert the logger change; the keeper is config in `deploy/cloud-ha/`.

### Dependencies

53.01, 53.02, 53.10. Needed by 53.16 and Epic 50.10.
