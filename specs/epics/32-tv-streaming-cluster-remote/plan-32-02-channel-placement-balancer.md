# Plan 32.2 — Channel placement and balancer — implementation

> Implementation plan for [story-32-02](story-32-02-channel-placement-balancer.md).
> Design: [design.md](design.md) §3. Uses slot 0121 (plan 32.1); no new migration.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The placement service runs in the **home's streaming process**, next to the HDHomeRun handler. | `/auto/v{n}` and the web port's `/stream/` already land there; one process decides, no extra hop. The API asks it over loopback (`/internal/tv/ensure`). |
| D2 | Decisions under `pg_advisory_xact_lock(hashtext('tv_place:'||channel))`; the generation from a sequence. | Safe even if two home processes run by mistake; a unique, growing fencing token. |
| D3 | Capacity is counted from placements, not from host reports. | A placement takes a slot when written; no overfill between beats. |
| D4 | Sticky placements; no rebalancing. | A TV channel's load is constant; moving interrupts viewers. |
| D5 | The engine gets a `Fence` interface; the home's fence is the resolver, a node's checks the header against the row. | Keeps `streaming/internal/channel` unaware of the cluster. |
| D6 | On SQLite (single machine) the placement store is in memory and always answers the home. | No nodes are possible there (Epic 31). |

## 1. Files

**Create**
- `streaming/internal/placement/placement.go` — `Service{Store, Hosts, Settings, Clock}`: `Resolve`, `Place`, `Release`, `Ack`
- `streaming/internal/placement/choose.go` — the policies (pure function, table-tested)
- `streaming/internal/placement/store_pg.go`, `store_mem.go`
- `streaming/internal/placement/notify.go` — `LISTEN tv_placement` (pgx), callbacks
- `streaming/internal/placement/*_test.go`

**Modify**
- `streaming/internal/channel/engine.go` — `Engine.Fence` (`Allow(ctx, channel) (gen int64, err error)`), `Ensure` calls it before `reg.admit`; stations keep `gen`; a 5 s ticker + NOTIFY callback stop superseded stations; `ReapIdle` → `Fence.Release(channel, gen)`
- `streaming/internal/channel/registry.go` — capacity follows the host's capacity (beat answer), `admit` still caps locally (belt and braces)
- `streaming/internal/channel/handler.go` — `writeEngineError` maps `placement.ErrNoHost{Reason}` to 503 with `X-Maktaba-Reason`
- `streaming/internal/hdhr/handler.go` — `Auto` unchanged here (proxying is 32.3); errors map to "all tuners in use"
- `streaming/main.go` — build the service on the home; a node gets a `NodeFence` (row check)
- `api/internal/handlers/rules/schema.json` — `tv_cluster.pins` (map slug → host name)

## 2. Algorithm

```go
func (s *Service) Resolve(ctx context.Context, ch uuid.UUID, want Need) (Placement, error) {
    if p, ok := s.cache.Get(ch); ok && s.hosts.Online(p.Host) { return p, nil }
    p, err := s.store.Get(ctx, ch)
    if err == nil && p.Active() && s.hosts.Online(p.Host) && want.SatisfiedBy(s.hosts.Get(p.Host)) {
        s.cache.Put(ch, p, 2*time.Second); return p, nil
    }
    reason := ReasonFirstRequest
    if err == nil && p.Active() { reason = ReasonFailover }
    return s.Place(ctx, ch, want, reason)
}

func (s *Service) Place(ctx, ch, want, reason) (Placement, error) {
    return s.store.Tx(ctx, func(tx Tx) (Placement, error) {
        tx.Lock("tv_place:" + ch.String())
        if p, err := tx.Get(ch); err == nil && p.Active() && s.hosts.Online(p.Host) { return p, nil }
        used := tx.UsedByHost()                         // placements in placing|live|moving
        h, why := Choose(s.hosts.Snapshot(), used, s.settings(), ch, want)
        if h == uuid.Nil { return Placement{}, ErrNoHost{Reason: why} }
        p := Placement{Channel: ch, Host: h, Gen: tx.NextGen(), State: Placing, Reason: reason}
        tx.Upsert(p); tx.Notify("tv_placement", p.String())
        return p, nil
    })
}
```

`Need` = `{Live bool, Push string}`: `Live` when `channel_programs` has a `kind = 'live'` block
within 24 h (one indexed query, cached 5 min per channel); `Push` set by the remote controller.

`Choose` implements design §3.3; it returns the reason when nothing fits (`all_full`,
`no_live_host`, `no_push_host`, `pin_skipped:<why>`), surfaced in 503s and the admin page.

## 3. Fencing on nodes

The signed front-door request carries `X-Maktaba-Placement`. `NodeFence.Allow` reads
`SELECT host_id, generation, state FROM channel_placements WHERE channel_id = $1`, requires
`host_id = self && generation = header`, then calls `tv_placement_ack`. The station check loop
(5 s, and NOTIFY on the node's own connection) re-reads the row and stops on mismatch.

## 4. Tests

- `choose_test.go`: table of hosts/used/settings → host or reason (home_first, balanced, spill_at
  75/100, pins, can_live, can_push, weights, ties).
- `placement_test.go` with `store_mem`: concurrent `Place` on one channel (one generation), on two
  channels with one free slot, failover reason, cache invalidation on NOTIFY.
- Postgres (skip if absent): advisory lock and sequence across two `Service` instances.
- `engine_test.go`: `Fence` refusing → no station; superseded generation → `Stop` within one tick;
  reap releases only its generation.

## 5. Rollout

With one host the resolver always answers the home, so this ships with no visible change. Turning
on `nodes_stream` and a node makes spill-over possible; `tv_cluster.policy` stays `home_first`.
