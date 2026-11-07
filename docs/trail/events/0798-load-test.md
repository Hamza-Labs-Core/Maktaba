---
schema: 1
kind: pr
pr: 798
title: Load test and sizing
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [780]
---

## What happened
- Story 50.14: `cloud/loadtest` and `maktaba-loadtest` (ramp, stop rules, collectors, regression compare, sizing rows, the target check). Also streamgen's stalls and TTFB, the rehearsal's `load-small` with a compare against main's last baseline artifact, `loadtest.yml` for the staging pair, `docs/cloud-sizing.md`, and the Phase 2 rules and alerts on the witness with promtool tests and a runbook. maktaba.dev's cloud guide covers it.

## What we learned
- Code that imports `cloud/internal/...` must live inside the cloud module. The spec's `tools/loadtest/` became `cloud/loadtest` and `cloud/cmd/maktaba-loadtest`.
- Prometheus 3's range selectors are open at the start: in a promtool test with samples 5 minutes apart, `rate(x[5m])` sees one sample and returns nothing. Sample more finely in the test (2 minutes here) rather than widening the rule's range. A recorded series keeps its name, so `exp_samples` needs `labels: 'name'`.
- `docker stats` gives CPU in units of one core (200 % is two full cores). Divide by the machine's CPUs for a share, and integrate the samples over time for CPU-seconds.
- A baseline "from main's last run" is an artifact. The job needs `actions: read` and `gh run download <id> --name ...`, and the first run (none yet) must pass, saying so.
- Docker Desktop restarted with no images: rebuild `maktaba-cloud:rehearsal` and pull the compose images before a local scenario.

## Open
- The absolute numbers wait for the owner's staging pair and a `loadtest.yml` run. Then the epic README §4.2, §5 and architecture §13.13 numbers, and `RELAY_MEASURED_*` on the witness, follow the measured row. IPv6 is tested with staging.
- 50.15, 50.19, 50.30 to 50.35, 53.16 to 53.18; 50.11's leftovers: the Apple and Android re-check after a failed request, and the Android TV strip. 50.13's T09 waits for a home stack in the cloud rehearsal.
