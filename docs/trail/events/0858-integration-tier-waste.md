---
schema: 1
kind: pr
pr: 858
title: The integration tier's waste cut, and its slowest tests in the log
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- The integration tier failed on time with every test passing (5m06s, 5m13s, 5m15s against 5m). Ten runs took 2m33s–5m15s for the same work, depending on the runner. The `api` module sets the tier's length (#855).
- `TestPGPlayQueue` walked a 1,301-video queue one move per item. It now moves to each window's last item: 44 moves, 63.7s to 2.7s, with the same checks.
- `queues`' 20 Postgres tests each ran every migration through goose. They now copy `clustertest`'s migrated template: 82s to 8.5s.
- `pgtest.Migrator` built maktaba-api into a new folder on every call. Each call was a full link, 6.5s on an M3 Ultra and more on a busy runner. It now builds once per process into one path per checkout, where `go build` of an up-to-date target does not link again (0.7s).
- `tools/ci/integration-go.sh` runs `go test -json`. Its log reads as before, followed by each module's ten slowest tests.

## What we learned
- `go build -o` into a path that already holds an up-to-date binary skips the link. A new temporary folder per call pays the whole link each time.
- A scratch database per test is cheap when it is a copy of a migrated template (`CREATE DATABASE … TEMPLATE`). It is expensive when it runs the migrations again.
- A test that drives an API through many small steps can cover the same ground in larger strides when each response shows a window.

## Keep doing
- New Postgres tests in `api` get their database from `clustertest.ScratchDB`. Tests in `cmd/maktaba-server` get maktaba-api from `pgtest.APIBinary` or `pgtest.Migrator`.
- Read the slowest-tests list in the integration job's log before raising its budget.

## Open
- `nodecopy`'s tests, in the replica job, still build maktaba-api into a temporary folder each.
- The budget stays at 5m; CI's numbers after this go into #855.
