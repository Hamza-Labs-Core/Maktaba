# Story 50.10 — Monitoring, the canary home and alerts

> Epic 50 · Phase 1 · Depends on: 50.07, [53.17](../53-logs-errors-reports/story-53-17-cloud-observability.md) · Needed by: 50.11, 50.12

## Why

Today nothing watches the cloud from outside. The relay exposes `/metrics`, but no one scrapes
it, and we would learn about an outage from a user. The SLO in README §4.2 (99.9 %) needs a
measurement that sees what a user sees: a request that goes through the relay, down a tunnel,
to a home, and back.

## Description

### 1. The canary home

On the witness W runs a **real cloudlink** (the same code homes run) linked to a cloud account
`canary` with slug `canary`, in front of a tiny stub that answers:

- `GET /api/health` → `200 {"ok":true,"t":<unix ms>}`
- `GET /canary/10mb.bin` → 10 MB of fixed bytes (checks throughput, not just reachability)

The canary home is an ordinary home to the cloud (it has a Pro plan granted by the admin,
which replaced its Family plan when 50.19 dropped Family, and is excluded from revenue
reports). Its bandwidth is small: the 10 MB file is fetched every 10
minutes from one network (≈ 43 GB a month).

### 2. Outside probes

Two probes on networks outside Hetzner, so a Hetzner-wide outage still pages:

| Probe | What | Every |
|---|---|---|
| Network 1: a hosted uptime service (recommended: UptimeRobot or Better Stack free tier; **the owner chooses**) | `https://canary.relay.maktaba.tv/api/health`, `https://api.maktaba.tv/readyz`, `https://status.maktaba.tv` | 30 s (the free tier's limit decides; 60 s acceptable) |
| Network 2: GitHub Actions cron (`.github/workflows/cloud-probe.yml`) | the same URLs plus the 10 MB file | 5 min (Actions' minimum), each run probing every 30 s for 5 min |

A **minute is down** when both networks fail the canary or the API in it (README §4.2). Each
probe pages on its own, so neither depends on our monitoring stack being up.

### 3. Metrics and alerts on the witness

Prometheus, Alertmanager and Grafana run on W and scrape A and B over the private network:
the cloud's `/metrics`, `postgres_exporter`, Patroni's `/metrics`, `node_exporter`, Caddy's
metrics, the probe results from network 2 (pushed to a Pushgateway), and the drill results
(50.09). 30 days of retention.

New metrics:

| Metric | From |
|---|---|
| `pg_replication_lag_bytes`, `pg_replication_lag_seconds` | postgres_exporter |
| `patroni_primary` (1 on the leader), `patroni_failovers_total` | Patroni |
| `fip_holder{node}` (1 on the node Hetzner reports as holder) | a 60 s check script on W (reads the Floating IP's server via the API, read-only) |
| `relay_held_requests_total{result=served|timeout}`, `relay_held_seconds` | relay (50.04) |
| `tunnel_drains_total{reason}`, `tunnel_drain_forced_total` | relay (50.03) |
| `cert_expiry_seconds{name}` | Caddy / blackbox exporter |
| `backup_age_seconds`, `wal_archive_failures_total`, `backup_restore_drill_ok` | 50.09 |
| `canary_up`, `canary_latency_seconds`, `canary_throughput_bytes_per_second` | blackbox exporter on W + network 2 |
| `maktaba_errors_total{service,code}`, `maktaba_http_requests_total`, `maktaba_http_request_seconds`, `maktaba_log_lines_total`, `maktaba_log_dropped_total`, `maktaba_log_forward_lag_seconds`, `maktaba_problem_reports_total` | the cloud processes; **named and emitted by Epic 53.17** (`shared/metrics/cloud-series.md`), scraped here |

**Logs and errors are not built here.** The cloud's logs (14 days on each node, forwarded to W,
searchable by the service admin) and its error groups come from
[Epic 53.17](../53-logs-errors-reports/story-53-17-cloud-observability.md). This story scrapes
53.17's application metrics, puts them on the dashboards, and owns every alert rule, including
the two below that use them.

### 4. Alert rules

| Alert | When | Severity |
|---|---|---|
| `canary_down` | 2 failed probes in a row (≤ 90 s) | page |
| `api_down` | 2 failed `/readyz` in a row | page |
| `slo_burn_fast` | 2 % of the month's 43.8 min budget burned in 1 h | page |
| `slo_burn_slow` | 5 % of the budget burned in 6 h | page |
| `failover_happened` | `patroni_failovers_total` increased | page (info: confirm it was healthy) |
| `replica_missing` | no streaming replica for 2 min | page |
| `replication_lag` | lag > 10 s for 2 min | warn |
| `fip_assign_failed` / `fip_mismatch` | callback failed, or the holder isn't the leader for 2 min | page |
| `cert_expiring` | < 14 days | warn; < 7 days page |
| `backup_stale` / `wal_archive_failing` / `restore_drill_failed` | 26 h / 5 min / drill not ok | page |
| `disk_filling` | > 80 % | warn; > 90 % page |
| `held_requests_timing_out` | > 1 % of held requests time out over 10 min | warn |
| `relay_near_capacity` | > 60 % of measured capacity (50.14) at peak, 3 days running | warn (Phase 2 trigger) |
| `cost_ceiling` | month's spend > 80 % of README §5 ceiling | warn (from 25.21) |
| `cloud_error_spike` | `maktaba_errors_total` rate (any `service`) over 5× its 7-day baseline for 10 min (Epic 53.17's counter) | warn; page if `service` is `cloud.relay` and it lasts 20 min |
| `log_forward_lagging` | `maktaba_log_forward_lag_seconds` > 60 for 5 min (a node's logs aren't reaching W) | warn |

Every alert has a `runbook_url` annotation pointing at its page in `docs/runbooks/` (50.12).

### 5. Paging

Alertmanager sends pages to the owner's phone through **ntfy** or **Telegram** (the owner
chooses), and warnings to email. The hosted probe pages through its own app/email. Pages repeat
every 15 min until acknowledged.

## User-facing strings

None here; users see the status page and banner (50.11), driven by these probes.

## Acceptance criteria

- **Given** the canary home is linked,
  **when** a probe fetches `https://canary.relay.maktaba.tv/api/health`,
  **then** the request goes through the Floating IP, Caddy, the relay, the canary's tunnel and
  back, and answers 200.
- **Given** the relay process is stopped,
  **when** two probes in a row fail,
  **then** the owner's phone gets a `canary_down` page within 90 s, with the runbook link.
- **Given** the witness W is down too,
  **when** the relay fails,
  **then** the hosted probe and the Actions probe still page.
- **Given** a failover happens,
  **when** it completes,
  **then** `failover_happened` reports which node is now leader and how long the canary was down.
- **Given** a month of probe data,
  **when** the SLO dashboard is opened,
  **then** it shows the month's availability, the budget left, and every down minute with its
  cause.
- **Given** any alert rule,
  **when** it fires,
  **then** its message has a `runbook_url` that exists in `docs/runbooks/`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `promtool test rules` on `alerts.yml` | synthetic series for each alert | fires at the stated thresholds, not before |
| T02 | unit | a test over `alerts.yml` | list `runbook_url`s | every file exists in `docs/runbooks/` |
| T03 | unit | canary stub | `GET /canary/10mb.bin` | exactly 10 MB, fixed SHA-256 |
| T04 | rehearsal (CI) | 50.06 `failover-kill-leader` with Prometheus in the compose | kill A | `canary_down` and `failover_happened` fire; resolve after |
| T05 | rehearsal (CI) | `db-blip` | Postgres restart | `canary_down` does **not** fire (the relay rides it out, 50.05) |

## Edge cases

- **Probe false alarms** from the probe provider's own network: needing two networks for a
  "down minute" keeps the SLO honest; each network still pages alone, which is noisier but
  safer.
- **The canary's home side breaks** (W rebooting): `canary_down` fires while customers are fine.
  The page says to check `api_down` and the relay's `tunnels_open` first; W's health is its own
  alert.
- **Cardinality.** `tunnel_bytes_total{server_id}` stays on its separate scrape (architecture
  §13.12), not in the default job.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/cloud-ha/monitoring/prometheus.yml`, `alerts.yml`, `alertmanager.yml` (new) | §3–§5. |
| `deploy/cloud-ha/monitoring/grafana/` (new) | SLO and cluster dashboards. |
| `deploy/cloud-ha/monitoring/alerts_test.yml` (new) | `promtool` tests. |
| `deploy/cloud-ha/canary/` (new) | The stub and its unit; cloudlink config. |
| `deploy/cloud-ha/bin/fip-check` (new) | `fip_holder` metric. |
| `deploy/cloud-ha/setup.sh` | Witness role installs the above. |
| `.github/workflows/cloud-probe.yml` (new) | Network-2 probe, pushes results, opens an issue on failure as a backup signal. |
| `cloud/internal/metrics/` | Metrics from 50.03/50.04 registered here if not already (Epic 53.17 adds its own there). |

### Tests

- `promtool` rule tests and the runbook-link test in `relay.yml`.
- **CI only:** alert checks inside `failover-kill-leader` and `db-blip`.

### Rollout

1. The owner picks the hosted probe and the paging channel.
2. `setup.sh --role witness` installs monitoring; the canary is linked by the owner from the
   admin page.
3. Start the 14-day clock of the launch gate (README §11) when the canary first goes green.

### Rollback

Monitoring changes are config: revert the file and reload Prometheus/Alertmanager. The outside
probes stay on regardless.

### Dependencies

50.07 (the nodes to watch and the witness to run on); Epic 53.17 (the cloud's application
metrics, logs and error groups). Needed by 50.11, 50.12.
