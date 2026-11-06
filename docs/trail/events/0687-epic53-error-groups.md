---
schema: 1
kind: pr
pr: 687
title: Epic 53 story 53.10 — error groups on the home
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The log index groups error and fatal lines by the cloud's fingerprint, now in `shared/log/go`. Each group keeps counts, hours, users and samples, and a group that comes back on a later version is new again. The API lists and changes the groups (audited), and the `errors_notifier` loop notifies admins.
- The keeper's `Close` waits for compressions still in flight.

## What we learned
- Moving a function the cloud already used into `shared/log/go`, with the cloud delegating to it, keeps the home and the cloud grouping alike. A test in the cloud asserts the two are the same.
- A flaky "directory not empty" TempDir cleanup came from a real shutdown bug: background zstd compression outlived `Keeper.Close`. Following the flake to its source found it.
- A notifier that belongs to a switchable component is a catalog loop of that component (`api:errors_notifier` under `logs.index`), so turning the component off stops it too.

## Open
- Crash reports (53.14) and the cloud's groups (53.17, after Epic 50) will feed the same table.
