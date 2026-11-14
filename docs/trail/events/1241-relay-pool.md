---
schema: 1
kind: pr
pr: 1241
title: "Epic 50 story 50.16: a pool of relays"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1241 merged as 727546b2b. Off until `[relay.pool] enabled = true`; with it off one relay
  behaves as before. With it on:
  - a `relays` table and `relay_standby_tunnels` (migration slot 00500018);
  - rendezvous hashing of the home id over the healthy relays: each home holds a primary tunnel
    to rank 1 and an idle standby to rank 2;
  - any relay answers for any home, forwarding over the private network to the primary, then
    the standby;
  - a lost relay's homes are served through their standbys within seconds and move their
    primary there; an added relay takes only the homes it wins, at most 20 a second per relay;
    a relay back from an outage waits 1 minute before taking homes back;
  - `GET /v1/homes` gives apps a `relay_hint`, `https://<slug>.rN.relay.maktaba.tv`;
  - `maktaba-cloud relay-dns` on the witness keeps `relay.maktaba.tv` and `*.relay.maktaba.tv`
    on the healthy relays, never removes the last record, and leaves records alone when it
    can't read the database or Cloudflare (tested against a fake Cloudflare only);
  - `maktaba-cloud relay list|drain|activate|down <id>`.
- Two rehearsal scenarios on three relays: `relay-pool-kill-one` and `rebalance-add-relay`.
- Three CI rounds: lint (`relay.RelayTarget` stuttered, an unused test helper), then the
  `relay-pool-kill-one` judging (below), then a merge of main after 50.17 (#1236) took slot
  00500015, `cloud/rehearsal/pool_test.go`, `compose-pool.yml`, `upPool` and scenario C17: the
  relay pool's became `relaypool_test.go`, `compose-relay-pool.yml`, `upRelayPool`, C18/C19 and
  slot 00500018.

## What we learned
- A balance bound must come from the binomial spread, not a round percentage: 1,000 random
  homes on 3 relays break ±10 % of a third in 6.7 % of runs with a perfect hash (5,000 trials),
  so CI would fail one run in fifteen. The bound is now 4 standard deviations (±18 % on 3
  relays, ±13 % on 2); T01 still proves the hash to ±2 % over 100,000 ids.
- Judge a failure by when it failed (start plus duration), not when its request started: the
  four "failed before the kill" requests started 60–180 ms before `docker kill` and broke at the
  instant the relay died, as the edge's log showed. `TestRelayPoolJudging` covers both rules.
- Placement after 1,000 homes connect at once takes about 40 s: each relay redirects about 40
  at once (its 20-a-second drain), the rest at the next 30-second check; misplaced homes stay
  reachable through forwarding meanwhile.
- Cloud migrations have no gap check (goose runs with `WithAllowMissing`): a higher slot may
  land before lower ones still in open PRs, but two open PRs can claim the same slot and the
  second to merge must move.

## Keep doing
- Prove a "skewed" result against the math before widening or narrowing a bound.
- Check main's newest cloud slot, rehearsal file names and scenario numbers right before each
  merge while several Epic 50 PRs are open.

## Open
- Owner actions before the pool serves (nothing deployed, no DNS or Cloudflare call made):
  relay machines `rN` on the private network with DNS-only records `rN.relay.maktaba.tv` and
  `*.rN.relay.maktaba.tv` (TTL 60); certificates for `*.relay.maktaba.tv` and each
  `*.rN.relay.maktaba.tv`; each member id and addresses in its `cloud.env`; `[relay.pool]
  enabled = true` with the measured `capacity_mbps` on every relay and the API; 443 open, the
  peer port and Postgres private only; on the witness the `maktaba-cloud` binary, a read-only
  role on `relays`, a zone-scoped DNS-edit token in a 0600 file and
  `maktaba-relay-dns.service` (`deploy/cloud-ha/relay-pool/README.md`).
- A staff-console relay page with drain and activate buttons in place of the shell commands.
