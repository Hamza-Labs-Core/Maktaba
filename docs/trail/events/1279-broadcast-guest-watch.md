---
schema: 1
kind: pr
pr: 1279
title: "Guest viewers on watch.maktaba.tv: watch pages, guide and TV pairing (story 50.27)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1279 merged as cb1ae8a05. The edge serves server-rendered watch pages (Arabic default,
  `/ar/` and `/en/`): a publisher's channel grid limited to what the credential allows, the
  player with now/next and the CC BY credit, the guide and short legal pages. A home publishes
  its guide with the ingest token; the edge never edits or ranks it. A test proves a publisher's
  pages link to nothing of another publisher (no directory).
- TV pairing: the TV gets a nonce, a poll key never shown in the QR code, and a code both
  screens show; the phone sends an invite, a publisher handle plus short code, or shares its open
  session (the TV gets its own token with the same id and expiry, never the phone's cookie).
  `WATCH_SITE_OFF=1` takes the pages down while playback stays up.

## What we learned
- The `changed reference images are listed` check reads the PR description: a stacked PR that
  carries another PR's screenshots must list them too, or it fails though the images are right.
- Most earlier red on this branch was its stale base; merging main fixed the Player UI tests
  and integration budget. Bring main into a long-lived stacked branch before reading its
  failures.

## Open
- An hls.js integrity hash and the watch page's Report flow into 50.24's queue; the App Watch
  screens (T03–T06).
