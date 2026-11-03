# Story 50.16 — A pool of relays

> Epic 50 · Phase 2 · Depends on: 50.08, 50.14 · Needed by: Phase 3

## Why

Phase 1 has one active relay. Story 50.14 measures how much it carries; when the active relay
passes **60 % of that capacity** at peak for a week, or at about 2,000 linked homes (README §7),
one machine is no longer enough, and the machine it runs on is still a pause in remote play
when it fails (up to 3 minutes of RTO).

Story 25.8 planned to scale by "consistent hash on `server_id` at the L4 LB" with a sidecar
that parses the bearer token. Hetzner's load balancer can't do that (round robin or least
connections only, and LB11 carries only 1 TB a month: [research.md](research.md) §4), so it was
never built. This story builds the pool another way: the cloud decides which relays a home
uses, any relay can answer for any home (50.04's directory), and apps get a shortcut to the
right one.

## Description

### 1. The relays table

```sql
CREATE TABLE relays (
  id            text PRIMARY KEY,          -- "r1", "r2", …
  public_ip     inet NOT NULL,
  private_ip    inet NOT NULL,             -- Hetzner private network
  state         text NOT NULL,             -- 'active' | 'draining' | 'down'
  heartbeat_at  timestamptz NOT NULL,
  capacity_mbps int NOT NULL               -- from 50.14's measurement
);
```

Each relay writes its heartbeat every **5 s**. A relay is **healthy** when `state = 'active'`
and its heartbeat is under **15 s** old. `draining` is set by the deploy or by the owner before
taking a relay out.

### 2. Assignment by rendezvous hashing

For a home `h` and each healthy relay `r`, score = `SHA-256(h.server_id ‖ r.id)` read as an
integer, weighted by `capacity_mbps` (weighted rendezvous: `-capacity / ln(score / 2^256)`).
The highest score is the home's **primary** relay, the second its **standby**.

- Deterministic: any node computes the same answer from the same relay list.
- Adding or removing a relay moves only the homes whose top two change (about 2/N of them for
  one relay among N).
- The relay list each node uses is cached for 5 s, so a new relay or a dead one is seen within
  20 s at most (15 s heartbeat age + 5 s cache).

### 3. Two tunnels per home

The tunnel handshake answers with the home's assignment:
`X-Maktaba-Relays: primary=r3.relay.maktaba.tv; standby=r5.relay.maktaba.tv`.

- The home holds its **primary tunnel** to `r3` and an idle **standby tunnel** to `r5` (PINGs
  only; same protocol). Both register in the tunnel directory (50.04), marked `primary` or
  `standby`.
- New requests go to the primary tunnel. If the primary relay dies, the directory's next
  lookup finds the standby (its row is live), and requests go there at once; the home then
  dials its new standby by the new assignment. This is the Cloudflare Tunnel idea (several
  connections to different data centres, research §3).
- A home that connects to the wrong relay (old address, round robin) gets
  `DRAIN{reason: "rebalance"}` with its assignment, and moves (50.03).
- Idle standby tunnels cost about 1 MiB of memory each on the relay (25.8 T03), so 10,000 homes
  add ~10 GiB spread over the pool.

### 4. Names, DNS and certificates

- Each relay has `rN.relay.maktaba.tv` (its public IP) and a wildcard certificate for
  `rN.relay.maktaba.tv` and `*.rN.relay.maktaba.tv`, issued by DNS-01 as in 50.02.
- Every relay also serves the stable names `relay.maktaba.tv` and `*.relay.maktaba.tv` with the
  shared wildcard certificate from 50.02.
- The stable names are **DNS round robin** over the healthy relays' public IPs. The witness
  runs `relay-dns`: every 15 s it reads the relays table and its own probe of each relay
  (`/readyz` on the public IP), and updates the `A` records through the Cloudflare API, TTL
  **60 s**. It never removes the last record. The Floating IP leaves the relays in Phase 2 (it
  stays on the database primary, 50.18).

### 5. Requests: hint first, any relay as fallback

- `GET /v1/homes` (50.13) lists a `relay_hint` connection
  `https://<slug>.r3.relay.maktaba.tv` (the primary relay) before the stable
  `https://<slug>.relay.maktaba.tv`.
- Any relay receiving a request for a home it doesn't hold looks the home up in the tunnel
  directory and forwards over the **private network** (50.04), which Hetzner doesn't bill. A
  hint that is out of date costs one extra hop, never a failure.
- A relay holding neither tunnel for the home and finding no live directory row holds the
  request up to 15 s (50.04), then `503 home_reconnecting`.

### 6. Rebalancing

When the healthy set changes (a relay added, drained or dead), each relay checks its own
tunnels every 30 s and sends `DRAIN{reason: "rebalance"}` to homes no longer assigned to it,
at most **20 homes a second per relay**, so a new relay fills in minutes without a storm. A
dead relay needs no drain: its homes are already on their standby tunnels.

### 7. Limits that must be global

- **Bandwidth caps** (25.12): `Meter.Allow` reads Postgres plus its own in-memory delta
  (`cloud/internal/billing/meter.go`). With N relays the cap can overshoot by at most what N
  relays carry in one 30 s flush. Accepted; documented in the plan page.
- **Concurrent streams** (`MaxConcurrentStreams` in `cloud/internal/billing/plans.go`): the
  relay doesn't enforce it today; only the billing API reports it. In the pool it must be
  counted across relays: a `relay_streams` row per open stream (home, relay, opened_at), written
  at stream open and deleted at close, with a 2-minute expiry swept by the worker. A stream is
  refused with `429 stream_limit` when the home's open count is at its plan's limit. Rows are
  written only for media streams (HLS sessions), not every request, so the write rate stays
  low.

## Acceptance criteria

- **Given** three healthy relays,
  **when** 1,000 homes connect,
  **then** each relay holds primary tunnels for its weighted share ±10 %, and each home holds a
  standby tunnel on a different relay.
- **Given** a home streaming through relay r3,
  **when** r3 is powered off,
  **then** the stream continues through the home's standby tunnel within **10 s** (README §4.2
  Phase 2 RTO ≤ 30 s), and the home has a new standby within 60 s.
- **Given** a relay is added,
  **when** it turns healthy,
  **then** homes move to it at no more than 20 a second per relay, with no failed request, and
  only homes whose assignment changed move.
- **Given** an app using a `relay_hint` that points at the wrong relay,
  **when** it sends a request,
  **then** the request succeeds through forwarding.
- **Given** a dead relay,
  **when** 30 s have passed,
  **then** its IP is gone from `relay.maktaba.tv`'s round robin.
- **Given** a Pro home at its concurrent-stream limit split across two relays,
  **when** a third device opens a stream on a third relay,
  **then** it gets `429 stream_limit`.
- **Given** the witness is down,
  **when** relays fail,
  **then** DNS keeps its last records (no removal), and forwarding plus standby tunnels keep
  homes reachable through the healthy relays still listed.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | rendezvous with 3, 4, 10 relays | 100k home IDs | shares within ±2 % of weights; adding one relay moves ≈ 1/N of primaries |
| T02 | unit | relay list cache | heartbeat ages 10 s / 20 s | healthy / not |
| T03 | unit | handshake | wrong relay | `DRAIN{rebalance}` with the assignment |
| T04 | integration | 3 relays in-process, simulated homes | kill one | streams continue on standby ≤ 10 s |
| T05 | integration | stream counting across 3 relays | open N+1 streams | last one `429 stream_limit`; rows expire 2 min after a crash |
| T06 | unit (witness `relay-dns`) | fake Cloudflare API | relay down / last relay down | record removed / last record kept |
| T07 | unit (cloud) | `GET /v1/homes` | pool on | `relay_hint` before `relay` |
| T08 | rehearsal (CI, 50.06 `relay-pool-kill-one`) | 3 relay containers, 1,000 simulated homes, stream generator | `docker kill` one relay | 0 failed requests after 10 s; p99 move ≤ 10 s |
| T09 | rehearsal (CI, 50.06 `rebalance-add-relay`) | 2 relays, 1,000 homes | start a 3rd | ≤ 20 moves/s per relay; 0 failed requests; final shares ±10 % |
| T10 | load (50.14) | pool of 3 | sustained load | forwarding hop adds ≤ 5 ms p95 |

## Edge cases

- **Split view of health.** Two nodes may briefly disagree on the healthy set (5 s caches).
  Forwarding makes this harmless: a request still reaches whichever relay holds the tunnel.
- **All relays but one die.** Every home's primary and standby fall to the survivor; the
  survivor may be over capacity, and 50.10's capacity alert fires.
- **Home with old software** (no two-tunnel support): it keeps one tunnel; the directory and
  forwarding still serve it; it just waits for a reconnect when its relay dies.
- **Private network down between relays.** Forwarding fails; hints still work for most
  requests; the relay answers `502` for a home it can't reach, and the alert in 50.10 fires.
- **Hetzner traffic accounting.** Included traffic is per server; rendezvous weights by
  capacity keep relays' public traffic roughly even, so no single relay runs past its 20 TB
  while others idle.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/migrations/` (new migration) | `relays`, `relay_streams`; `relay_tunnels` (from 50.04) gains `role` (`primary` / `standby`). |
| `cloud/internal/relay/pool.go` (new) | Heartbeat, healthy list cache, weighted rendezvous. |
| `cloud/internal/relay/ws.go` | `X-Maktaba-Relays` on handshake; rebalance drain for wrongly placed homes. |
| `cloud/internal/relay/registry.go`, `directory.go` (50.04) | Standby tunnels; prefer primary; fall to standby. |
| `cloud/internal/relay/rebalance.go` (new) | Periodic check, rate-limited drains. |
| `cloud/internal/handlers/relay/proxy.go` | Stream counting and `429 stream_limit` for media streams. |
| `cloud/internal/billing/streams.go` (new), `cloud/cmd/maktaba-cloud/role_worker.go` | Global stream count; sweeper. |
| `cloud/internal/handlers/homes/homes.go` | `relay_hint` connection. |
| `cloud/internal/cloudlink/health.go`, `conn.go` | Primary + standby tunnels from the assignment. |
| `deploy/cloud-ha/relay-pool/` (new) | Relay node role (Caddy with per-relay and shared certificates, cloud `--role=relay`), private-network firewall. |
| `deploy/cloud-ha/witness/relay-dns/` (new) | The DNS round-robin controller (a small Go command in `cloud/cmd/relay-dns/`, new). |
| `specs/architecture.md` §13.3 | Replace "consistent hash on server_id at the LB" with the pool design. |

### Tests

- T01–T07 in `relay.yml`'s `test` job (integration tier for T04–T05).
- **CI only:** rehearsal scenarios `relay-pool-kill-one` and `rebalance-add-relay` (50.06),
  with a fake Cloudflare API for `relay-dns`.
- T10 in 50.14's load harness.

### Rollout

1. Ship pool code switched off (`relay.pool.enabled = false`): one relay behaves as Phase 1.
2. Owner adds a second relay node and its DNS name (README §12 style; nothing created by
   code); turn the pool on; watch rebalancing fill it.
3. Move the Floating IP off the relay path; the stable names become round robin.
4. Home software with two-tunnel support ships in a regular release; the fleet page (25.20)
   shows how many homes have a standby tunnel.

### Rollback

- `relay.pool.enabled = false` sends every home back to one relay (they rebalance by drain);
  point the stable names back at the Floating IP.
- Stream limits: `relay.stream_limit.enabled = false` stops refusing (counting continues).

### Dependencies

50.08 (deploys that drain, which the pool reuses) and 50.14 (the measured capacity that
weights the hash and triggers the phase). Builds on 50.02 (certificates), 50.03 (`DRAIN`) and
50.04 (directory and forwarding).
