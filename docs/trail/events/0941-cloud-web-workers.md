---
schema: 1
kind: pr
pr: 941
title: The app and console Workers on Workers Routes
date: 2026-11-08
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [928, 883]
---

## What happened
- Story 50.32 part 2. `maktaba-cloud-web` (app.maktaba.tv) and `maktaba-admin-web` (admin.maktaba.tv) each sit in front of their bundle (`run_worker_first`, `workers_dev = false`), attached by Workers Routes on the proxied names, as 50.31's handover required.
- Each passes only its host's API paths to the origin and answers the rest 404 itself. Both put §5's headers on every answer, assets and API alike.
- The admin Worker also passes the console's four sign-in routes until 50.33. A vitest reads `role_api.go` and keeps that list equal to the listener's `ConsoleSignIn`.

## What we learned
- The spec's table for the admin Worker lists only `/v1/staff/*`, `/v1/admin/*` and `/healthz`. Following it to the letter would have locked the console out until 50.33, because its sign-in runs on the account routes. When a story ships ahead of the one that replaces a stopgap, carry the stopgap through every layer (listener, Worker, both tests).
- `web/`'s tsconfig has no Node types. A test that reads a file loads `node:fs` through a dynamic import, as `src/styles/posters.test.ts` does.
- `wrangler deploy --dry-run` with the devportal's pinned version (via npx) checks a Worker's bundle and config without an account or token.

## Open
- 50.34 deploys both Workers (`cloud-web-deploy.yml`) and smoke-checks them.
- 50.33 replaces the console's sign-in routes in both layers.
