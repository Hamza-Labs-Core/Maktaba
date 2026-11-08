---
schema: 1
kind: pr
pr: 957
title: 53.16: the Apple release uploads symbol tables; the cloud deploy provisions their token
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
---

## What happened
- The TestFlight workflow turns each app's dSYMs into symbol tables after its upload and sends the tables (never the dSYMs) with the `MAKTABA_SYMBOLS_TOKEN` repository secret, labelled with the tag.
  - With no secret, the step logs one line and does nothing else, so the job stays green.
  - An upload that fails with a token only warns: the app is already in TestFlight, so a release is never lost to the symbol service. The tables are kept 90 days as the run's `symbols-N` artifact, and `maktaba-symbols upload` accepts kept tables to send later.
- The cloud gets the token the way it gets the Cloudflare Tunnel token: `relay.yml` passes the secret to `deploy/cloud-ha/deploy.sh`, which writes `/etc/maktaba/symbols.token` on both api nodes over SSH and names it in `cloud.env`; the deploy restarts the api. The single-VPS stack runs only the relay role and needs nothing.
- The one owner step (add the secret) is in `docs/testflight.md`, with `deploy/cloud-ha/README.md` and the secrets-rotation runbook.

## What we learned
- A release step that talks to an optional service gates on the secret being non-empty and warns on failure, so neither a missing secret nor an outage can fail a release.

## Open
- No symbols are uploaded until the owner adds `MAKTABA_SYMBOLS_TOKEN`.
