# Story 32.3 — Front door

## Description

*As a Plex user, I want Plex to keep using one tuner at one address whatever machine encodes the
channel; as a browser viewer, I want the web app to keep playing from its own origin.*

The home is the only address clients see. It serves discovery, guide and lineup itself and
forwards each channel request to the host that encodes the channel.

Design: [design.md](design.md) §4.

## Acceptance criteria

- **AC1** SSDP, `/device.xml`, `/discover.json`, `/lineup.json`, `/lineup_status.json`,
  `/lineup.post`, `/xmltv.xml` and `/lineup.m3u` are served by the home as today; their URLs always
  point at the home.
- **AC2** `/auto/v{n}` and `/stream/channel/{id}/live.ts` resolve the placement and, for a node,
  copy the node's TS through a splicing copier: no buffering beyond one hub chunk, flushed per
  write; the tuner lease is taken and released on the home.
- **AC3** HLS playlists are always proxied; segments are proxied when `tv_cluster.hls_mode = proxy`
  (default) or answered with `307` to a signed node URL valid for 60 s when `redirect`.
- **AC4** The web port's `/stream/` gate is unchanged (session check, path allowlist, cookie
  stripping); it proxies to the home's streaming service, which forwards as in AC2/AC3.
- **AC5** Requests to nodes carry `X-Maktaba-Front` (HMAC-SHA256 over method, path and expiry with
  the cluster stream key) and `X-Maktaba-Placement`; nodes reject missing, expired or wrong
  signatures with 403 and log `event=tv_front_reject`.
- **AC6** Three consecutive proxy failures to a host mark it down at once (32.4 takes over); a
  failed playlist request is retried once after re-resolving.
- **AC7** Placements are cached in the front door for 2 s and dropped on NOTIFY or any proxy
  error; a segment fetch costs no database query.
- **AC8** Local channels (placed on the home) take today's in-process path with no extra hop.
- **AC9** `GET /api/tv/info` still reports the home's addresses only.

## Test cases

- **TC1** `TestAutoProxiedToNode` — a fake node TS server; a Plex-like client reads TS from the home.
- **TC2** `TestHLSProxyAndRedirectModes` — playlist proxied in both modes; segment 200 vs 307 + signature.
- **TC3** `TestSignedRequestVerify` — good, expired, tampered path, wrong key.
- **TC4** `TestLeaseCountedOnHome` — tuner count 2 → the third `/auto` gets "all tuners in use"
  although the node has room.
- **TC5** `TestNoBufferingTS` — first bytes reach the client within 100 ms of the upstream writing them.
- **TC6** `TestLocalPathUnchanged` — a home placement → no proxy hop.

## Edge cases

- **EC1** Plex's probe-then-reconnect: the warm grace on the node and the cached placement make
  the second connection instant.
- **EC2** Redirect mode with a client that cannot reach the node (another VLAN): segment 307s
  fail; the docs say to keep `proxy` unless every client reaches every node.
- **EC3** Home NIC load: 20 tuners × 6.5 Mb/s ≈ 130 Mb/s through the home in proxy mode; the
  metric `maktaba_tv_frontdoor_bytes_total` shows it.

## Dependencies

32.1, 32.2.
