# Plan 32.4 — Failover and drain — implementation

> Implementation plan for [story-32-04](story-32-04-failover-drain.md).
> Design: [design.md](design.md) §6. No migration (uses `streaming_hosts.draining`, added in slot 0122).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Down = missed beats past `dead_after` **or** three proxy failures. | Beats catch a silent host; proxy failures catch a dead one fast. |
| D2 | A sweeper on the home re-places only channels with demand. | Idle channels would be reaped anyway; re-placing them wastes encoders. |
| D3 | TS clients are spliced, HLS clients get a discontinuity. | Plex keeps its tuner; HLS players handle `EXT-X-DISCONTINUITY`. |
| D4 | Flapping hosts (two down/up in 10 min) are held out for 10 min. | Avoid moving channels back and forth. |
| D5 | Move and drain reuse `Place(…, reason)`. | One code path for every re-placement. |

## 1. Files

**Create**
- `streaming/internal/placement/sweeper.go` — every 2 s: hosts newly down → re-place demanded placements
- `streaming/internal/placement/demand.go` — demand per channel: front-door clients attached (splice copiers, HLS touches within the warm grace kept by the front door), remote pushes (`tv_remote_pushes` live)
- `api/internal/handlers/tvcluster/tvcluster.go` — `PUT /api/tv/cluster/hosts/{id}`, `POST …/channels/{id}/move`, `DELETE …/channels/{id}/placement` (admin); the API calls the home streaming's `/internal/tv/place` on loopback for move/stop
- tests

**Modify**
- `streaming/internal/frontdoor/splice.go` — on upstream EOF/error with the client alive: `Resolve` (which re-places when the host is down) and continue; count splices
- `streaming/internal/channel/station.go` — start option `Discontinuity bool` → `MuxerArgs(restart=true)` for the first muxer of a failover station
- `streaming/internal/placement/hosts.go` — flapping history; `Online` honours `draining` for new placements only
- `api/internal/router/*.go` — mount `tvcluster`

## 2. Steps

1. Host state machine in `hosts.go`: `online → down` (beats or failures), `down → online` (a beat,
   unless flapping), `draining` flag from the row.
2. Sweeper: diff of online sets; for each newly down host, `SELECT channel_id FROM
   channel_placements WHERE host_id = $1 AND state IN ('placing','live','moving')`; for each with
   demand `Place(ch, need, ReasonFailover)`; else `Release`.
3. Failover start: the station for a `failover` placement starts with `Discontinuity: true`.
4. Splice: `for attempt := 0; attempt < 3; attempt++ { copy(upstream) ; if client gone → return ;
   p = Resolve() }`; logs `event=tv_splice`.
5. Drain: the admin API sets `draining`; `Choose` skips draining hosts; "move now" =
   `Place(ch, …, ReasonDrain)` for each placement on the host.
6. `tv_cluster.failover = false`: the sweeper only releases; requests still re-place via `Resolve`.

## 3. Tests

Fake clock + `store_mem`: dead after 15 s; three failures; demanded-only re-placement; comeback
host's stations stopped by fencing; flapping hold-out; drain blocks new placements; move-now.
Splice test with two httptest upstreams (A closes after 1 MB, B continues). E2E (CI job with two
streaming processes and SQLite → skip; Postgres job): kill the node process, the TS client keeps
reading within 20 s.

## 4. Rollout

On by default (`tv_cluster.failover = true`) — it only acts when a node is in use. Runbook: drain
before maintenance (32.12).
