# Plan 32.5 — Admin: streaming hosts and on air — implementation

> Implementation plan for [story-32-05](story-32-05-admin-hosts-on-air.md).
> No migration. Reads slot 0121 tables, `channel_runtime` (0083 + 0117), `tv_remote_pushes` (0122, when present).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | One read endpoint `GET /api/tv/cluster` for the whole page. | One poll every 5 s; the page is admin-only and small. |
| D2 | The page lives in `Admin/LiveTV.tsx` as two sections (hosts, on air), split into components. | TV admin stays in one place (Epic 27); `LiveTV.tsx` is already the TV readiness page. |
| D3 | Stale `channel_runtime` rows are cleaned by the home, not by the page. | The audit's P1-5: nothing cleans after a crash today. |

## 1. Files

**Create**
- `api/internal/handlers/tvcluster/get.go` — `GET /api/tv/cluster` (admin): settings, hosts with health and reasons, placements joined with `channel_runtime`, channels (number, name), pushes
- `web/src/lib/tvClusterApi.ts` — types + calls (get, put host, move, stop)
- `web/src/pages/Admin/LiveTV/StreamingHosts.tsx`, `OnAir.tsx`, `MoveDialog.tsx`
- tests: `api/internal/handlers/tvcluster/get_test.go`, `web/src/pages/Admin/LiveTV/*.test.tsx`

**Modify**
- `web/src/pages/Admin/LiveTV.tsx` — mount the two sections; poll while visible (`document.visibilityState`)
- `web/src/pages/Admin/Channels.tsx` (channel form) — "Prefer host" select → `tv_cluster.pins`
- `web/src/i18n/en.json`, `ar.json` — `tv.cluster.*`
- `streaming/internal/placement/hosts.go` — `CleanStaleRuntime` at boot and every minute: delete `channel_runtime` rows without an active placement on that host, or whose host is down for > 1 min

## 2. API shape

```json
{
  "settings": {"nodes_stream": true, "policy": "home_first", "spill_at": 100, "hls_mode": "proxy"},
  "hosts": [{"host_id": "…", "role": "home", "name": "Mac-Studio", "state": "online",
             "reason": null, "enabled": true, "draining": false, "encoders": ["videotoolbox","software"],
             "capacity": 16, "capacity_override": null, "used": 5, "weight": 1,
             "load": {"cpu": 0.31}, "can_push": {"rtmps": true, "srt": false}, "can_live": true,
             "version": "…", "last_beat_at": "…", "stream_base_url": "http://192.168.1.20:8081"}],
  "on_air": [{"channel_id": "…", "number": 105, "name": "…", "host_id": "…", "generation": 42,
              "state": "live", "reason": "first_request", "since": "…", "encoder": "videotoolbox",
              "viewers": {"hls": 2, "tuners": 1, "remote": 0}, "restarts": 0, "nvenc_fallbacks": 0,
              "push": null, "stale": false}]
}
```

`viewers.hls` from the front door's touch tracker (sent by the home streaming in its beat
`load.viewers`), `tuners` from the home's lease registry, `remote` from `tv_remote_sessions`.

## 3. Tests

API: shape, admin-only, host PUT validation. Web: switch, drain, move dialog (host list excludes
down/draining hosts), stop confirm, Arabic snapshot, phone width (container queries).

## 4. Rollout

Visible as soon as 32.1 lands (the home alone); node rows appear when nodes beat.
