# Story 50.04 — Any relay process answers for any home

> Epic 50 · Phase 1 · Depends on: 50.03 · Needed by: 50.07, 50.08, 50.16

## Why

A home's tunnel lives in one relay process's memory (`cloud/internal/relay/registry.go`). During
a deploy there are two processes (blue and green, 50.08): new client requests reach the new
process while some homes are still on the old one, for up to 10 s at 100 homes a second. After
a failover the new leader has no tunnels at all until homes reconnect. In Phase 2 there are
several relays, and a client may reach any of them. Today all three cases end in
`503 server_offline` for a home that is fine.

This story adds a **tunnel directory** (which process holds each home's tunnel), lets a relay
**forward** a request to the process that holds the tunnel, and **holds** a request for up to
15 s while a home is reconnecting.

## Description

### 1. The directory

New table in the cloud database:

```sql
CREATE TABLE relay_tunnels (
    slug         text PRIMARY KEY,       -- servers.slug
    server_id    uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    relay_id     text NOT NULL,        -- e.g. "a-blue", "a-green", "r3"
    relay_addr   text NOT NULL,        -- where peers reach it: "127.0.0.1:8444", "10.0.1.3:8443"
    seq          bigint NOT NULL,      -- the tunnel's seq (registry.go)
    connected_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE relay_nodes (
    relay_id     text PRIMARY KEY,
    relay_addr   text NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    draining     boolean NOT NULL DEFAULT false
);
```

- Written in the same place the registry already records a server online or offline
  (`Registry.Up` / `Registry.Down` `record` callbacks): Up upserts the row when the new `seq` is
  higher; Down deletes it only if `relay_id` and `seq` still match (so a newer tunnel elsewhere
  is never erased).
- Each relay process writes `relay_nodes.heartbeat_at` every 5 s. A row whose heartbeat is older
  than **20 s** counts as dead: lookups ignore it, and any live relay deletes its
  `relay_tunnels` rows (the dead process's homes will reconnect anyway).
- Each relay keeps a **2 s cache** of directory lookups, so a burst of requests for one home
  costs one query.
- `relay_id` and `relay_addr` come from config (`relay.id`, `relay.peer_addr`); the blue/green
  units set them (50.08).

### 2. Forwarding to a peer

When a request for `<slug>` arrives and the local registry has no tunnel (or only a draining
one is local and a newer one is elsewhere):

1. Look up the directory (cache, then Postgres; 50.05 covers a database outage).
2. If another live relay holds it, proxy the request there over plain HTTP on the peer address
   (loopback in Phase 1, the Hetzner private network in Phase 2), streaming both ways, with:
   - `X-Maktaba-Relay-Hop: 1`, and
   - `X-Maktaba-Relay-Auth: <HMAC-SHA256(k_peer, relay_id ‖ slug ‖ unix_seconds)>` where
     `k_peer = HKDF(MAKTABA_CLOUD_TOKEN_SECRET, "relay-peer-v1")`, valid ±30 s.
3. The peer serves it from its own registry only. A request that already has
   `X-Maktaba-Relay-Hop` is **never forwarded again** (one hop at most: no loops). A bad or
   missing auth header on the peer port gets `403`.
4. The peer listener (`relay.peer_addr`) binds only to loopback or the private interface, never
   the public one; UFW and the Hetzner firewall also block it publicly.
5. Metering and tier checks happen **once**, on the relay that owns the tunnel (so bytes are not
   counted twice). The forwarding relay counts `relay_forwarded_bytes_total` for capacity only.

### 3. Holding during a reconnect

If no live relay holds the tunnel, but the home was online in the last **2 minutes**
(`server_health.last_heartbeat`, or the directory row deleted in that window), the relay
**holds** the request: it waits for the slug's tunnel to appear (locally, or in the directory)
for up to **15 s**, checking every 250 ms locally and every 1 s in the directory. A held request
that finds the tunnel goes on as normal. One that doesn't gets
`503 home_reconnecting` with `Retry-After: 5`.

- At most **20 held requests per home** and **5,000 per process**; past that, `503` at once.
- Homes offline longer than 2 minutes get today's `503 server_offline` at once.

### 4. The relay's error page, in Arabic too

`cloud/internal/handlers/relay/page.go` renders English only (`lang="en"`). It gets the new
`home_reconnecting` text and picks Arabic from `Accept-Language` (`dir="rtl"`), for this and the
existing kinds.

## Acceptance criteria

- **Given** home H's tunnel is on process blue and a request for H reaches process green,
  **when** green looks up the directory,
  **then** it forwards the request to blue over loopback, the client gets the home's answer, and
  the bytes are metered once (on blue).
- **Given** a request carrying `X-Maktaba-Relay-Hop: 1` reaches a relay without H's tunnel,
  **when** it handles the request,
  **then** it does not forward it again: it holds or answers `503`.
- **Given** a request to the peer port with no or a bad `X-Maktaba-Relay-Auth`,
  **when** it arrives,
  **then** the peer answers `403` and logs `relay_peer_auth_failed`.
- **Given** H was online 30 s ago and has no tunnel anywhere,
  **when** a request for H arrives and H reconnects 4 s later,
  **then** the request is answered normally about 4 s late.
- **Given** H was online 30 s ago and doesn't come back,
  **when** 15 s pass,
  **then** the client gets `503 home_reconnecting` with `Retry-After: 5`, and a browser gets the
  page in its language.
- **Given** H has been offline for 10 minutes,
  **when** a request arrives,
  **then** the relay answers `503 server_offline` at once.
- **Given** relay process blue crashes,
  **when** 20 s pass without its heartbeat,
  **then** its `relay_tunnels` rows are removed and no request is forwarded to it.
- **Given** a newer tunnel for H registered on green,
  **when** blue's older tunnel for H goes down,
  **then** the directory still points at green.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | directory upsert/delete with seqs | Up(seq 5 on green), Down(seq 4 on blue) | row stays green/5 |
| T02 | unit | peer auth | good, stale (40 s), wrong key, wrong slug | ok, 403 ×3 |
| T03 | integration | two relay processes, one home on blue | request via green | home's body; meter rows on blue only |
| T04 | integration | hop header set, no local tunnel | request | not forwarded; held then 503 |
| T05 | integration | home seen 30 s ago, reconnects at 4 s | request | 200 after ~4 s |
| T06 | integration | home seen 30 s ago, never back | request | 503 `home_reconnecting`, `Retry-After: 5` at 15 s |
| T07 | integration | 21 held requests for one home | request | the 21st gets 503 at once |
| T08 | integration | kill blue's heartbeat | wait 20 s | blue's rows gone; green answers by hold |
| T09 | unit | `page.go` | `Accept-Language: ar` | Arabic heading, `dir="rtl"` |
| T10 | rehearsal (CI) | 50.06 `deploy-blue-green` and `failover-kill-leader` | as there | 0 failed requests in deploy; held requests counted in failover |

## Edge cases

- **Peer slower than the client.** The forwarding relay streams with the same per-stream window
  as the tunnel (64 KiB); a slow peer slows only that request.
- **WebSocket requests** (client WS through the relay, frame `0x30`) are forwarded as an HTTP
  upgrade to the peer; same hop and auth headers.
- **Directory points at a relay that just died.** The forward fails to connect within 1 s; the
  relay marks that row stale in its cache and falls into the hold path.
- **Clock skew between peers** beyond 30 s breaks peer auth; chrony on every node (50.07) and an
  alert on skew > 5 s.
- **The database is down.** The directory can't be read; 50.05 makes the relay fall back to its
  last cached directory entries and the hold path.

## User-facing strings

| Key | English | Arabic |
|---|---|---|
| `relay.page.home_reconnecting.heading` | Your home is reconnecting | الجهاز الرئيسي يعيد الاتصال |
| `relay.page.home_reconnecting.message` | Your home lost its connection to Maktaba Cloud for a moment and is coming back. This page tries again in a few seconds. | انقطع اتصال الجهاز الرئيسي بسحابة مكتبة لحظة وهو يعود الآن. ستعيد هذه الصفحة المحاولة بعد ثوانٍ. |
| `relay.page.retry` | Try again | إعادة المحاولة |

The existing kinds in `page.go` (`server_offline`, `tunnel_error`, `tunnel_timeout`,
`tier_limit`) get Arabic in the same change.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/migrations/<next free slot>_relay_directory.sql` (new; the latest today is `00150001_usage_telemetry.sql`) | `relay_tunnels`, `relay_nodes`. |
| `cloud/internal/relay/directory.go` (new) | Upsert/delete by seq, heartbeat, stale cleanup, 2 s cache. |
| `cloud/internal/relay/registry.go` | Call the directory from the Up/Down record callbacks. |
| `cloud/internal/relay/peer.go` (new) | Peer listener, HMAC auth, one-hop rule. |
| `cloud/internal/handlers/relay/proxy.go` | Local → directory → forward → hold → 503. |
| `cloud/internal/handlers/relay/hold.go` (new) | Hold with limits. |
| `cloud/internal/handlers/relay/page.go` | `home_reconnecting`; Arabic via `Accept-Language`. |
| `cloud/internal/config/config.go` | `relay.id`, `relay.peer_addr`, `relay.hold_max` (15 s), `relay.hold_recent` (2 min). |
| `cloud/cmd/maktaba-cloud/role_relay.go` | Start the peer listener and heartbeat. |
| `cloud/internal/metrics/` | `relay_forwarded_total`, `relay_forwarded_bytes_total`, `relay_held_total{result}`, `relay_peer_auth_failed_total`. |

### Tests

- T01–T09 in `cloud/internal/relay` and `cloud/internal/handlers/relay` (`relay.yml` test job).
- **CI only:** rehearsal scenarios `deploy-blue-green`, `drain-1k` and `failover-kill-leader`
  (50.06) assert forwarding and holding end to end.

### Rollout

Ship with `relay.peer_addr` empty (no forwarding, hold only). 50.08 sets it when it introduces
blue/green.

### Rollback

`relay.peer_addr = ""` and `relay.hold_max = 0` return to today's behaviour. The tables can stay.

### Dependencies

50.03 (draining tunnels: the directory must prefer the newer tunnel while the old one drains).
