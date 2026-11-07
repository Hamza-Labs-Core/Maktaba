---
schema: 1
kind: pr
pr: 714
title: The relay rides out a database blip: home cache, buffered metering, lenient readiness
date: 2026-11-06
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [705]
---

## What happened
- Story 50.05: the relay serves known homes from a cache (triggers NOTIFY every change), buffers metering through an outage without losing a byte, and its `/readyz` stays `200 degraded` for 5 minutes of database outage. The containerised db-blip rehearsal went from 200 failed requests to 0 in four rounds.

## What we learned
- lib/pq does not stop a connection's startup when the context ends. Against a database that vanished (a killed container, a failover's old address) a connect hangs until `connect_timeout`, or the kernel's TCP timeout without one; `QueryRowContext` with a 500 ms context still waited. `db.Open` now adds `connect_timeout=2`, and nothing on a request's path reads the database for a home the relay already knows.
- A health check must never do the work it reports on: the readiness ping itself took the connect timeout, longer than Caddy's health timeout, so both relay processes left the edge at once. Check in the background, answer from the last result.
- The old meter replaced its counter map before a flush and dropped the snapshot when the flush failed: every failed flush lost up to 30 s of metering.
- Triggers are the reliable way to NOTIFY: they cover every writer (billing webhook, admin suspension, unlink) without each handler remembering.
