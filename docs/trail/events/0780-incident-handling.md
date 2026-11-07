---
schema: 1
kind: pr
pr: 780
title: Incident handling, runbooks and one-click recovery
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [727, 739]
---

## What happened
- Story 50.12: severities `sev1`–`sev3` and a runbook on every alert, with Alertmanager routing SEV1 every 5 minutes, SEV2 once and SEV3 daily. The cloud runbooks moved to `docs/runbooks/cloud/`, all in one layout, with templates for users in both languages and a postmortem template. The Cloud switchover, failover and DNS swap workflows run `deploy/cloud-ha/runbook/`, which the rehearsal runs first (`runbook-*`). Also a lint (T01–T03, T07), a postmortem reminder, the Billing page's no-credit text, and maktaba.dev's "When something goes wrong".

## What we learned
- Write a recovery script so it reaches a node through a hook (`NODE_EXEC`, after `deploy.sh`'s pattern). Then the workflow uses SSH, the rehearsal uses `docker exec`, and a shell test uses a stand-in that answers as Patroni would, all with one script. The rehearsal's `/etc/maktaba/cloud-ha.env` has no `NODE_ADDR`, so the leader check takes `PATRONI_URL`.
- The fake Hetzner API checks its bearer token before any path, its own switches included. A rehearsal-only control (`POST /fake/outage`) needs the token too.
- A template people copy into `status/data/incidents/` must be a valid post: the guidance comment goes after the front matter (the builder needs `---` first). The status builder's test loads every template.
- Runbooks name real commands only. `maktaba-cloud` has `admin grant|revoke|list` today; a staff `disable` (50.33) and a billing reconcile (25.14) don't exist yet, and the runbooks say so.
- Docker Desktop may be stopped on this machine: `open -a Docker`, then wait for `docker info`.

## Open
- `tunnel_connections_low` and the `staff_*` alerts come with 50.31 and 50.33; their runbooks are ready.
- The game day (launch gate item 2) on the real pair, once it exists.
- 50.13 to 50.15, 50.19, 50.30 to 50.35, 53.16 to 53.18; 50.11's leftovers: the Apple and Android re-check after a failed request, and the Android TV strip.
