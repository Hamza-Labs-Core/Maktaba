# Story 35.10 — Routing on a node

## Description

*As a user, I want every page of Maktaba to work when I open a node's address — the library, the
player, Live TV, search, the admin pages — and to be told clearly when that machine cannot serve me
and where to go instead.*

Every API route gets a placement class: `local` (this machine), `any` (served from the database by
whichever API gets it) or `home` (proxied to the home with the delegated session). A node's web port
sends live TV to the home's front door, proxies images from the home's caches into a local disk
cache, and shows a paused page when the schema guard pauses it or the home is unreachable.

Design: [design.md](design.md) §11; README R6.

## Acceptance criteria

- **AC1** Every route mounted in `api/internal/router` declares a class (`placement.Local`,
  `placement.Any`, `placement.Home`); a router test walks all routes and fails on an unclassified one;
  the default when building is `home`. On the home every class is served locally.
- **AC2** On a node, `home` routes are reverse-proxied to `MAKTABA_HOME_API_URL` with the delegation
  headers (story 35.9), `X-Maktaba-Delegated-Session`, `X-Forwarded-For`, `X-Forwarded-Host` and
  `X-Maktaba-Via: node`; WebSocket upgrades (the hub socket `/api/cluster/ws`) and SSE work
  (`FlushInterval: -1`); `Domain` is stripped from `Set-Cookie`; an incoming `X-Maktaba-Via` is
  stripped.
- **AC3** `any` routes are served by the node from its database role when the schema guard fits;
  paused → 503 `{code: "paused", reason}`. Writes stay `home` in v1.
- **AC4** `/stream/…` on a node's web port passes the node's stream gate (session as
  `stream_gate.go`) and is proxied to the home's front door (`MAKTABA_HOME_STREAM_URL`) with per-write
  flush; `/xmltv.xml` and `/lineup.m3u` are proxied unchanged (their URLs name the home). Live TV is the
  only stream that goes through the home.
- **AC4a** VOD is served by the node itself: its streaming role reads the file from the NAS through
  its own mounts and the share-path resolver (story 31.3); no library media passes through the home or
  the cluster link. Only when the node's storage check cannot read the share is VOD served through a
  ticketed stream from a machine that can (design §11.5, story 35.16), and the Machines page says so.
- **AC5** Images from home-only caches (posters, sprites, chapter thumbs, review pages) are cached
  at `<data_dir>/cache/proxy/<sha256(url)>` honouring `ETag`/`Cache-Control`; content-addressed art
  is immutable; LRU eviction at `proxy_cache_mb` (2048).
- **AC6** The paused page names the reason (schema guard text, `home_unreachable`, removed) and links
  to the home's address; it reloads when the node's version changes; `local` routes keep working.
- **AC7** The pages matrix of design §11.4 holds: each page loads on a node in the E2E suite.

## Test cases

- **TC1** `TestEveryRouteHasPlacementClass` (router walk).
- **TC2** `TestHomeProxyAddsSignatureAndStripsDomain`, `TestHomeProxyWebSocketUpgrade`,
  `TestHomeProxySSEFlush`, `TestHomeProxyStripsIncomingVia`.
- **TC3** `TestAnyRoutePausedReturns503`.
- **TC4** `TestNodeStreamGateProxiesToFrontDoor` (extends `serve_proxy_test.go`).
- **TC5** `TestProxyCacheETagRevalidateAndLRU`.
- **TC6** `PausedPage.test.tsx` (reasons, link, reload on version change; en + ar).
- **TC7** Playwright on a node: Library, Video, Player, Live TV, Guide, Search, Processing, Settings,
  Changes, Updates load and act (proxied writes succeed).

## Edge cases

- **EC1** During a rollout the node is one release behind: `home` answers may carry new fields; the
  SPA ignores unknown fields (additive APIs).
- **EC2** Home reachable but slow: proxy timeout 30 s for JSON, none for streams; 504 shown as a toast.
- **EC3** Cache disk full: the proxy serves uncached and logs once.
- **EC4** A route moved from `any` to `home` in a newer release: the node's older router still serves
  it locally while its schema fits — acceptable because migrations are additive.

## Transport

On the cluster link (story 35.16) the home proxy sends `home` requests on the machine's control
channel and WebSocket upgrades as ticketed data streams; the signed HTTP proxy is the legacy path
while `cluster.link.mode = prefer` and the machine has no certificate.

## Dependencies

35.8 (node mode, schema guard), 35.9 (delegation); Epic 32 front door.
