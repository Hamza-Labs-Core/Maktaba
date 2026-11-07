---
schema: 1
kind: pr
pr: 715
title: The cloud rehearsal: the cloud's cluster in Docker, broken on purpose in CI
date: 2026-11-06
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [705, 714]
---

## What happened
- Story 50.06: `cloud-rehearsal.yml` runs two relay processes behind Caddy on Postgres in Docker, with 1,000 simulated homes and a stream generator run by Go scenario tests. drain-1k and db-blip are live and pass; eight scenarios wait for 50.02, 50.07, 50.08 and 50.09. `tools/release/cloud-guard.sh` gates every cloud deploy on it.

## What we learned
- Built before the stories it tests had merged, the harness found five bugs in them that unit and in-process tests passed (#705, #714 list them). Real containers, a real edge with health checks, and a database that vanishes rather than refuses connections are what found them.
- A killed container is not a refused connection: its address stops answering, so connects hang. Tests that stop Postgres cleanly (RST) never show it.
- The relay's agent dials with its servers row's UUID; a simulated home's made-up id only works against a fake store (`simhome.Options.ServerID`).
- Compose: a `MAKTABA_CLOUD_RELAY_PEER_ADDR` value looked wrong in the logs, but the logger was redacting the IP. Read the effective config with `docker compose config`, not the log.

## Open
- The tag guard requires the cloud rehearsal once 50.07's scenarios are live.
