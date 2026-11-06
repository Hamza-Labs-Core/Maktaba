---
schema: 1
kind: pr
pr: 571
title: Integration tests learn the server's bound ports
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge de0b76b98. Flaky `TestServeIntegration` (streaming and api, plus api `TestServeWithAuth_JWKSAndHeaders`). `runServe` opened its public and admin ports in separate goroutines, and the tests waited only on the admin port before hitting the public one (connection refused on #562). `freeAddr` also picked a port, closed it and passed the number on.
- Fix: `runServe` opens both ports first (`listenPair`) and reports the real addresses through a test-only `onListening` hook. Tests use `startServe(t)` on 127.0.0.1:0; `freeAddr`/`waitForPort` are gone.

## Keep doing
- A test gets a server's ports from the server once they are open. Never pick a port, release it and poll.
