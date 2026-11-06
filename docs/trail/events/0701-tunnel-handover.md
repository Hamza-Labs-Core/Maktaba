---
schema: 1
kind: pr
pr: 701
title: Homes move their relay tunnel without a gap (DRAIN, resume tickets, jitter)
date: 2026-11-06
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [700]
---

## What happened
- Story 50.03: the relay drains its tunnels on SIGTERM (DRAIN `0x22`, 100 homes a second, 150 s grace), homes reconnect with HMAC resume tickets (TICKET `0x23`) instead of argon2id, and reconnects use full jitter. `cloud/internal/relay/simhome` runs real agents as simulated homes; 1,000 moved between two in-process relays in 2.2 s with no failed request.

## What we learned
- Docker's default stop grace is 10 s: a relay that drains for up to 150 s needs `stop_grace_period` on its compose service, or SIGKILL drops every tunnel mid-drain. 50.08's blue/green deploy must keep it.
- `http.Server.Shutdown` returns while hijacked websockets stay open; the registry has to track every tunnel (a draining one beside its replacement too) to know when a process is empty.
- The story's AC "no one-second window holds more than 15 % of 1,000 reconnects" contradicts its own first wait of `[0, 2 s)` (about half of them in any second). §3 was followed; the test checks uniformity per 100 ms.
- The tunnel's auth is the in-band AUTH frame, not an HTTP header: the spec's `X-Maktaba-Tunnel-Features` and `401 ticket_invalid` are AUTH's `features` and `AUTH_FAIL ticket_invalid`.

## Stop doing
- `git reset --soft origin/main` to squash a WIP commit after a fetch: origin/main had moved, and the commit silently reverted three merged pull requests (caught by the affected-tests gate listing files the branch never touched). Reset to the branch's own base sha (from the reflog).
