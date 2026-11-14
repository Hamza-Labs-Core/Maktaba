# The relay pool (Epic 50 story 50.16)

Phase 2 of the relay: N relay machines (`r1`, `r2`, …) instead of the leader's one relay. The
pool is off until `[relay.pool] enabled = true`; with it off, one relay behaves as before.

```
relay.maktaba.tv, *.relay.maktaba.tv ──► DNS round robin (relay-dns on W) ──► any healthy relay
r1.relay.maktaba.tv, *.r1.relay.maktaba.tv ──► r1 only (a home's tunnel, an app's relay hint)
every relay ◄── private network ──► every relay (forwarding, 50.04's directory) and Postgres
```

## How it works

- Each relay heartbeats its row in the `relays` table every 5 s. A relay is healthy when its
  state is `active` and its heartbeat is under 15 s old (by the database's clock).
- Weighted rendezvous hashing of the home's server id over the healthy relays (weights:
  `capacity_mbps`) ranks them: the first is the home's **primary** (its tunnel), the second
  its **standby** (an idle second tunnel). Adding or removing a relay moves only the homes
  whose first or second place changed.
- A home's AUTH_OK carries its assignment; a home on the wrong relay is sent DRAIN with its
  assignment (at most `rebalance_rate` homes a second, checked every `rebalance_every` and as
  soon as the healthy set changes). A relay back from a blip waits 1 min before it takes its
  homes back.
- Any relay answers for any home: a request on a relay without the home's tunnel is forwarded
  over the private network to the relay holding the primary, else the standby.
- When a relay dies its homes' standbys answer within seconds, and each home dials its
  standby as its new primary.
- `GET /v1/homes` gives apps a `relay_hint` connection, `https://<slug>.rN.relay.maktaba.tv`,
  tried before the round-robin name.

## Adding a relay machine

1. A CPX-class machine on the private network, with this repository's relay units and Caddy
   (`deploy/cloud-ha/systemd/maktaba-cloud-relay@.service`, `caddy/Caddyfile`).
2. DNS (by hand, once): `rN.relay.maktaba.tv` and `*.rN.relay.maktaba.tv` → the machine's
   public IPv4, DNS only (not proxied), TTL 60 s.
3. Certificates: Caddy on the machine holds `*.relay.maktaba.tv` (shared by every relay) and
   `*.rN.relay.maktaba.tv` (its own), both by DNS-01 as in 50.02.
4. `/etc/maktaba/cloud.env` gains the member's values (`relay.env.example`); `cloud.toml`
   gains `[relay.pool] enabled = true` with `capacity_mbps` from 50.14's measurement.
5. Firewall: 443 open to everyone; the relay's peer port (`relay.peer_addr`) and Postgres only
   on the private network.
6. Start the relay units: the member writes its row and takes homes at once; the others move
   only the homes it won (about 1/(N+1) of them).

The api and every other relay need `[relay.pool] enabled = true` too (the api to give hints).

## Taking one out

```
maktaba-cloud relay list                 # members, states, heartbeats, capacity
maktaba-cloud relay drain r3             # no new homes; its homes move at the rebalance rate
maktaba-cloud relay down r3              # out of the pool at once
maktaba-cloud relay activate r3          # back in (it waits 1 min to settle)
```

## relay-dns on the witness

`maktaba-cloud relay-dns` keeps the round-robin names in step (`maktaba-relay-dns.service`,
`relay-dns.env.example`): adds before it removes, never removes the last record, and keeps the
records as they are when it cannot read the relays table or Cloudflare. It needs, on W, the
`maktaba-cloud` binary, a read-only database role on `relays`, and a Cloudflare token scoped
to Zone → DNS → Edit on the zone, in its own 0600 file. The Floating IP stops being on the
relay path once the names point at the pool.

## Proven in CI

The cloud rehearsal runs three relays behind a round-robin edge (`tools/rehearsal-cloud/
compose-pool.yml`) with a fake Cloudflare: `relay-pool-kill-one` and `rebalance-add-relay`
(`docs/cloud-rehearsal.md`).
