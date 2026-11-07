---
schema: 1
kind: migration
title: 53.17's cloud Logs and Errors moved to the separate operator console when main came in
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [852, 859]
---

## What happened
- After #859 landed, bringing main into #852 (53.17) stopped on six conflicts. Main had moved the operator console out of the user portal into its own app (`web/admin`, served at `admin.maktaba.tv` with routes at its root), and had made the worker's control port its only listener.
- Resolved as follows:
  - The Logs and Errors pages moved to `web/admin/src/pages/Logs.tsx` with `@cloud-shared/*` imports, at `/logs` and `/errors` in the console's nav and routes.
  - Their 40 strings moved into `web/admin`'s catalogs.
  - The portal's `App.tsx` and catalogs took main's side.
  - The test moved beside the console's other page tests.
  - The worker's `/metrics` went onto main's single control server.
- The cloud runbooks' saved searches, and the runbook lint's pattern, now point at `https://admin.maktaba.tv/logs?…` and `/errors`.

## What we learned
- A page added on a long-running branch has to follow a move made on main. Merge main early in a big epic branch: the conflict shows up as an "added by us" file on the old path, not as a conflict marker.
- Check URLs in docs too: a lint that pins a host and path fails nothing until the host moves.

## Open
- Stories 53.16 and 53.17's spec text still names `app.maktaba.tv/admin/reports` and `/admin/errors`. Changing it means updating their Arabic in `docs/wiki/db/ar/` too.
- Trail event 0685's open item (the Apple player's own sentence when streaming is down) is done: `WatchScreen.swift` shows `player.unavailable` from a225243d5.
