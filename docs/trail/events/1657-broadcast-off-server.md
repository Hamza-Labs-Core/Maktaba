---
schema: 1
kind: pr
pr: 1657
title: "Broadcast and moderation routes answer nothing while broadcasting is off (cloud and home API)"
date: 2026-11-24
session: f722afbf-3be4-4126-aa58-02c6e1ab9fb0
agent: claude-code
---

## What happened
#1624 hid broadcasting; its review found the servers behind it still answering. #1657 closes them:
- **Cloud:** one middleware (`cloud/internal/handlers/broadcastgate`) on the root of the user and console listeners. While `broadcast.enabled` is off, every broadcast, edge and moderation path answers what an unknown path answers. Its `exceptions` table is the whole list of what stays open: the edge's revocation pull, the kill switch, the test-publisher grant, and a test publisher's rehearsal routes.
- **Home:** `/api/broadcast/access*` needs the licence's broadcast feature, after the admin check.

## What we learned
- **The gate has to sit on the root router.** A middleware in a chi group runs only for a matched route and method, so a wrong method would answer 405 and tell that the route exists. On the root it runs first, and `http.NotFound` is byte for byte the unknown path's answer.
- **CORS answers before the gate.** `FormCORS` answered the report form's preflight by path, so its paths are dropped while broadcasting is off.
- **A server secret's check is argon2id**, and it runs only for an id that exists. Reading the test-publisher mark first keeps the answer's time from telling which ids are homes.
- **`chi.Walk` plus the handler's Go name finds a route whatever its path is.** `runtime.FuncForPC` on the `ChainHandler`'s endpoint names the package, so the route table test fails on a broadcast handler mounted outside the gate.
- **14 integration tests relied on routes that answered with broadcasting off.** `broadcastOn` opens the gate in a test.

## Keep doing
- Proving a guard test by breaking the guard: dropping two paths from the gate made the route table test name each open route.

## Open
- The operator console's Moderation page shows its error state while broadcasting is off.
- The edge's own ingest while `BROADCAST_ENABLED` is off is #1653's.
