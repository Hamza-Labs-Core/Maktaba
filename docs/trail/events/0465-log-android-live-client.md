---
schema: 1
kind: pr
pr: 465
title: "48.9: the Android client's Live calls"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Android client Live (`apps/android/client/…/api/Live.kt`): now, guide, one channel's guide, logo / poster / stream URLs, `tv/info`, tune, heartbeat, end, and `LiveSource` (LAN or relay from `tv/info.remote.via_relay`).
`ApiClient.kt` is unchanged.
Tested with JVM tests (`LiveApiTest` on MockWebServer, `LiveSourceTest` on virtual time with a fake home), and once against a branch stack (scratch Postgres, branch API): every route decoded. The stack has no Cloudflare, so tune answered 503 `remote_unavailable` as expected.

## What we learned
- Decided: a `LiveApi(client)` wrapper, as Android TV's `TvApi` does, not new methods in `ApiClient.kt` (its `send` is private and the file is no story's). It reuses `ApiClient.request` (internal: base path and headers), `playbackToken` and `refreshAfter`.
- Decided: a heartbeat's new token does not change `LiveSource.stream`, so the player is not reopened. Only `token` changes, and `LiveSource.interceptor` writes it into each Cloudflare request, as hls.js does on the web.
- Opening a new channel ends the old session first, so the per-user stream limit never trips.
- On a fresh DB, `POST /api/channels` answered 201 but wrote no `channels` row (TV staging, it seems). The stack check seeded a channel by SQL.
