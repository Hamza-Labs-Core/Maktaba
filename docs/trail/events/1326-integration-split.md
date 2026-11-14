---
schema: 1
kind: pr
pr: 1326
title: "The integration tier runs on three runners"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1326 merged as 0fc45af6d. CI's integration tier now runs as three jobs: `integration (api)`
  (the api module), `integration (rest)` (cmd/maktaba-server, streaming, the small modules and
  the cloud tests) and `pipeline` (`make test-integration-py`, its own 3m budget, plus the
  pytest steps that followed the tier). `make test-integration` still runs everything side by
  side locally. `tools/ci/tests/test_integration_split.py` pins that every Go module is in
  exactly one shard. No budget went up.
- Before: main's single job took 3m42s–4m57s, and #1283 failed at 5m02s with every test passing.
  After, on #1326's own run: 2m32s (api), 2m12s (rest), 48s (pytest).

## What we learned
- The tier's wall was the api module's tests (249–295 s), slowed by four `go test -p 4`
  processes, pytest and Postgres sharing one 4-vCPU runner. pytest's 120–147 s of collection
  on CI was CPU starvation: 9 s cold on a machine to itself, with no heavy imports.
- A brief's premise (here: pytest is the long pole) can be wrong; measure before cutting.
- pytest's `--durations` report was printing all along, buried after the Go output; it now
  ends its own job's log.
- #1303 had shown that trimming pytest alone doesn't move a tier bound by another job.

## Keep doing
- Prove a CI shape change by its first run's walls against each budget before merging.

## Open
- Two more runners per integration run.
