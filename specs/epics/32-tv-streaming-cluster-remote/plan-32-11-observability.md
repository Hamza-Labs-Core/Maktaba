# Plan 32.11 — Observability — implementation

> Implementation plan for [story-32-11](story-32-11-observability.md).
> No migration (uses `health_samples`, slot 0089). Design: [design.md](design.md) §10.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Each component registers its metrics where it runs (streaming: hosts, placements, front door, pushers; API: remote controller). | Matches the existing per-service `/metrics` (`api/internal/middleware/metrics.go`, `streaming/internal/channel/registry.go`). |
| D2 | The home writes a few `health_samples` per minute. | The control centre already charts `health_samples`; no new store. |
| D3 | No user or IP labels. | Cardinality and privacy. |

## 1. Files

**Create**
- `streaming/internal/placement/metrics.go`, `streaming/internal/frontdoor/metrics.go`, `streaming/internal/channel/pusher_metrics.go`
- `api/internal/tvremote/metrics.go`
- `api/internal/tvremote/health.go` — minute ticker → `health_samples` (`tv_hosts_up`, `tv_channels_on_air`, `tv_remote_viewers`, `tv_remote_pushes_live`, `tv_remote_cost_usd`)
- `web/src/pages/Admin/LiveTV/RemotePanel.tsx` + test
- tests: metrics registered with the right labels; no secrets in logs (capture `slog` output during a fake push)

**Modify**
- `api/internal/handlers/tvremote/admin.go` — `GET /api/tv/remote` returns inputs, pushes, sessions (user display names, devices), month usage, last 20 events (from the log ring used by the Logs page)
- `web/src/pages/Admin/SystemHealth.tsx` — the TV line
- `web/src/pages/Admin/LiveTV.tsx` — mount the Remote panel

## 2. Events

`slog` with `event=` keys from design §10; fields `channel_id`, `host_id`, `push_id`, `code`. The
pusher logs its ffmpeg command with the key replaced by `***`.

## 3. Tests

Metric names/labels; `health_samples` rows each minute (fake clock); log capture has no key/token;
Remote panel states (no integration, ready, live pushes, errors), Arabic.

## 4. Rollout

With each phase: the cluster metrics with 32.1–32.4, remote metrics with 32.7–32.10.
