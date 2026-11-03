# Story 50.03 — Homes move their tunnel without a gap

> Epic 50 · Phase 1 · Depends on: — · Needed by: 50.04, 50.07, 50.08

## Why

Today a home's tunnel ends only by breaking: a deploy kills the relay process, the WebSocket
dies, every in-flight request gets a 502, and the home reconnects with backoff 1 s, 2 s, 4 s …
up to 30 s (`cloud/internal/cloudlink/health.go`, no jitter). Every home retries at the same
instants, and each reconnect costs a bcrypt check (~50 ms of CPU, story 25.8 T10). A thousand
homes reconnecting together is 50 CPU-seconds of bcrypt on a 2-vCPU machine just as it comes
up.

This story lets the cloud **ask** a home to move: open a new tunnel first, then let the old
one finish its streams ("make before break"). It also makes reconnects cheap (a resume ticket
instead of bcrypt) and spread out (jitter).

## Description

### 1. The `DRAIN` frame (cloud → home)

New frame type `0x22 DRAIN` in the tunnel protocol (architecture §13.4,
`cloud/internal/relay/protocol.go`, `cloud/internal/cloudlink/frame.go`):

```
0x22 DRAIN   { "reason": "deploy" | "rebalance" | "shutdown",
               "within_ms": 5000,            // open the new tunnel within this time
               "deadline_ms": 120000,        // the old tunnel closes after this, streams or not
               "ticket": "<resume ticket>" } // see §2
```

On `DRAIN` the home:

1. waits a random time in `[0, within_ms)` (spreads the reconnects),
2. dials a **new** tunnel to the same relay URL, presenting the ticket,
3. once the new tunnel is up (handshake `101` and the first `PONG`), stops sending new work to
   the old tunnel; the old tunnel keeps serving the streams it already has,
4. closes the old tunnel with `4000 normal_closure` when it has no open streams, or at
   `deadline_ms`, whichever comes first.

The relay side marks the drained tunnel **draining**: the registry keeps it for its in-flight
streams but routes new requests to the newer tunnel (by `seq`, as `Registry.Up` already
compares). The newer tunnel's arrival no longer closes the older one with `4003 superseded`
when the older one is draining; it closes it only when the older one was not draining (a home
restart, as today).

A home that doesn't understand `0x22` (older home software) ignores it. The relay then falls
back to today's behaviour at `deadline_ms`: it closes the tunnel and the home reconnects with
backoff. The home software version that understands `DRAIN` is announced in the handshake
(`X-Maktaba-Tunnel-Features: drain,ticket`), so the relay knows which homes need the fallback.

### 2. Resume tickets

A **resume ticket** lets a home reconnect without a bcrypt check:

```
ticket = base64url( server_id ‖ issued_at ‖ expires_at ‖ HMAC-SHA256(k_ticket, server_id ‖ issued_at ‖ expires_at) )
k_ticket = HKDF(MAKTABA_CLOUD_TOKEN_SECRET, "tunnel-resume-v1")
```

- Valid **10 minutes**. The relay sends a fresh one every **5 minutes** over the tunnel
  (new frame `0x23 TICKET`) and in every `DRAIN`.
- Bound to one `server_id`. The relay checks the ticket's HMAC and expiry in microseconds,
  then checks in its cache (or Postgres) that the server isn't revoked or suspended.
- Every node derives the same key from the shared token secret, so a ticket from node A works
  on node B after a failover (50.07). That is what makes a failover's reconnect storm cheap.
- The home keeps the latest ticket **in memory only**. After a home restart it has none, and
  uses its bearer token (bcrypt) as today.
- A revoked server's tickets stop working at once (the revocation check above), and rotating
  the token secret invalidates every ticket (homes fall back to their bearer).

### 3. Jittered reconnect

`cloudlink.Backoff` gets **full jitter**: the wait for attempt *n* is a random value in
`[0, min(cap, base × 2^(n-1))]`, base 1 s, cap 30 s. The first attempt after a dropped (not
drained) tunnel waits a random `[0, 2 s)` instead of a fixed 1 s.

### 4. Graceful shutdown of the relay process

On `SIGTERM` the relay:

1. stops accepting new tunnels (`503` on `/v1/relay/ws`, so homes go to the other process or
   node),
2. sends `DRAIN{reason: "shutdown"}` to every tunnel, spread at **100 homes a second**,
3. keeps serving in-flight requests on draining tunnels,
4. exits when no tunnel is left, or at `server.shutdown_grace` (raised from 20 s to **150 s**
   for the relay role), whichever comes first.

`http.Server.Shutdown` doesn't close hijacked WebSocket connections, so the relay tracks its
tunnels itself (the registry already holds them) and closes them as above.

## Acceptance criteria

- **Given** a home with an open tunnel and two streams in flight,
  **when** the relay sends `DRAIN{within_ms: 5000, deadline_ms: 120000}`,
  **then** the home opens a new tunnel within 5 s, both streams finish on the old tunnel with
  every byte delivered, new requests go over the new tunnel, and the old tunnel closes with
  `4000` once its streams end.
- **Given** a draining tunnel whose stream is still open at `deadline_ms`,
  **when** the deadline passes,
  **then** the relay closes the old tunnel, and that stream's client gets a broken connection
  that its player retries (counted in `tunnel_drain_forced_total`).
- **Given** a home presents a valid resume ticket,
  **when** it upgrades `/v1/relay/ws`,
  **then** the handshake does no bcrypt check, takes under 5 ms of CPU at p99, and
  `tunnel_handshakes_total{method="ticket"}` increments.
- **Given** a ticket that is expired, has a bad HMAC, or belongs to a revoked or suspended
  server,
  **when** a home presents it,
  **then** the relay answers `401 ticket_invalid`, and the home retries at once with its bearer
  token.
- **Given** a ticket issued by node A,
  **when** the home presents it to node B (same token secret),
  **then** B accepts it.
- **Given** 1,000 simulated homes on one relay process,
  **when** the process gets `SIGTERM`,
  **then** every home is on the new process within 15 s, no client request fails, and the old
  process exits within 150 s.
- **Given** a home software version without `drain` in `X-Maktaba-Tunnel-Features`,
  **when** the relay drains,
  **then** the relay closes its tunnel at `deadline_ms` (or at once if it has no streams) and
  the home reconnects by backoff as today.
- **Given** a tunnel drops without a `DRAIN` (crash, network),
  **when** 1,000 homes reconnect,
  **then** their attempts are spread by jitter: no one-second window holds more than 15 % of
  them (today: all of them in the same instant).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `cloudlink.Backoff` with a seeded RNG | 10k draws per attempt | uniform in `[0, min(30 s, 2^(n-1) s)]` |
| T02 | unit | ticket mint/verify | good, expired, bad HMAC, other server | ok, `expired`, `bad_mac`, `wrong_server` |
| T03 | unit | protocol | encode/decode `0x22`, `0x23` | round-trips; unknown fields ignored |
| T04 | integration | home + relay in-process, 2 slow streams | `DRAIN` | both streams complete; new tunnel used for a third request; old closed `4000` |
| T05 | integration | draining tunnel, a stream that never ends | deadline 2 s (test value) | closed at 2 s; `tunnel_drain_forced_total` = 1 |
| T06 | integration | old home (no `drain` feature) | `DRAIN` | closed at deadline; reconnect by bearer |
| T07 | integration | revoked server holding a valid ticket | reconnect with ticket | `401 ticket_invalid`; bearer also refused |
| T08 | integration | 1,000 simulated homes (`cloud/internal/relay/simhome`) | `SIGTERM` | all moved ≤ 15 s; 0 failed requests; exit ≤ 150 s |
| T09 | benchmark | ticket handshake | 10k handshakes | p99 CPU < 5 ms; no bcrypt call |
| T10 | rehearsal (CI) | 50.06 scenario `drain-1k` | as T08, across two containers | as T08 |

## Edge cases

- **`DRAIN` twice.** A second `DRAIN` while the home is already moving is ignored; the new
  tunnel will get its own if needed.
- **The new tunnel fails to open.** The home keeps using the old tunnel (it was never closed),
  retries with jitter, and the relay's deadline still applies.
- **Clock skew.** Tickets are checked against the relay's clock only (issued and verified by
  the cloud); the home never interprets the times.
- **Ticket theft.** A ticket is as good as the home's bearer for 10 minutes, for that one home.
  It travels only inside the TLS tunnel, is never logged, and is kept in memory only.
- **A request arrives at the new process before the home has moved.** Handled by 50.04
  (forward to the process that holds the tunnel, or hold up to 15 s).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/relay/protocol.go`, `cloud/internal/cloudlink/frame.go` | Frame types `0x22 DRAIN`, `0x23 TICKET`. |
| `cloud/internal/relay/ticket.go` (new) | Mint and verify; HKDF key from the token secret. |
| `cloud/internal/relay/ws.go` | Accept `ticket` as well as bearer; read `X-Maktaba-Tunnel-Features`; send `TICKET` every 5 min. |
| `cloud/internal/relay/registry.go` | `draining` state; new tunnels don't supersede draining ones; `Drain(rate)` for shutdown. |
| `cloud/internal/relay/tunnel.go` | Track in-flight streams; close at zero or at the deadline. |
| `cloud/cmd/maktaba-cloud/role_relay.go`, `cloud/internal/server/shutdown.go` | SIGTERM: refuse new tunnels, drain, wait; relay `shutdown_grace` 150 s. |
| `cloud/internal/cloudlink/health.go` | Full jitter; handle `DRAIN` (dial new, move, close old); keep the ticket in memory. |
| `cloud/internal/cloudlink/conn.go` | Present the ticket on dial; announce features. |
| `cloud/internal/relay/simhome/` (new) | Simulated homes for tests and the rehearsal (also used by 50.06 and 50.14). |
| `cloud/internal/metrics/` | `tunnel_drains_total{reason}`, `tunnel_drain_forced_total`, `tunnel_handshakes_total{method}`. |
| `specs/architecture.md` §13.4 | Add the two frame types. |

### Tests

- Unit and integration tests above, in `cloud/internal/relay` and `cloud/internal/cloudlink`
  (run by `relay.yml`'s `test` job).
- **CI only:** rehearsal scenario `drain-1k` (50.06): two relay containers behind Caddy,
  1,000 simulated homes, a stream generator; sends `SIGTERM` to one; fails on any failed
  request or a move slower than 15 s.

### Rollout

1. Ship the relay side first (it accepts tickets and sends `DRAIN`, but homes without the
   feature get the fallback).
2. Ship the home side in the next Maktaba release (cloudlink is part of the home's server).
3. Until most homes have it, deploys still drop old homes at the deadline; the admin fleet page
   (25.20) shows how many homes lack `drain`.

### Rollback

- Relay: config `relay.drain.enabled = false` returns to today's close-on-shutdown behaviour.
- Home: an older home simply doesn't announce `drain`; nothing to undo.
- Tickets: `relay.ticket.enabled = false` makes every handshake use the bearer again.

### Dependencies

None. 50.04 (forwarding between processes) and 50.08 (deploys) build on it.
