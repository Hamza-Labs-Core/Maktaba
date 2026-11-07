---
schema: 1
kind: pr
pr: 883
title: The origin only through Cloudflare, and the trusted client IP (Epic 50, 50.31)
date: 2026-11-07
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [867]
---

## What happened
- `server.trusted_proxies` (CIDRs) replaces `server.trust_proxy_headers`, which config parse now refuses by name. `CF-Connecting-IP` is believed only from a trusted peer. The api never reads `X-Forwarded-For`. The access log uses the resolved IP, because `WithClientIP` is mounted before the logger.
- The relay's own `clientIP` is unchanged. It still reads the rightmost `X-Forwarded-For` entry Caddy appends and never reads `CF-Connecting-IP` (test T03).
- With `server.env = "production"`, a public listener is refused, and so is an empty `trusted_proxies`.
- `tools/cloudflare/tunnel.mjs` plans and applies the Tunnel's ingress and DNS records. A `maktaba-cloudflared` unit follows the leader.
- The session limit hit while the work was pushed but had no PR. The next session reran the tests, opened #883 and filed the rehearsal scenarios as #884.

## What we learned
- The shared access logger read the leftmost `X-Forwarded-For` entry, which the client controls, so any client could write its own address into the logs. Logging the resolved IP, with the TCP peer as the fallback, closes that. On the relay router the log now shows Caddy's loopback address.
- The WIP commit left `affected-tests` red: `role_api.go`, `main.go` and `ratelimit.go` changed with none of their tests. The fix was a real test through `newAPI`'s wiring, not a `No-Test-Change` trailer: a forged `CF-Connecting-IP` from an untrusted peer shares the peer's rate-limit bucket, and behind the trusted connector two clients get separate buckets. Dropping `WithTrustedProxies` makes it fail.
- A new step for T04 in the Caddy job piped into `grep -q`, which the repo's lint forbids. Check new workflow shell against the lint rules before pushing.

## Open
- Rehearsal scenarios T06–T08 (the Tunnel end to end, leader failover, the aop fallback): #884.
