---
schema: 1
kind: pr
pr: 474
title: "48.12: signed stream URLs pass the web port's gate"
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Closes G2. `stream_gate.go` lets a `?sig=` request with no credentials through on `/stream/{session}/…`, `/stream/direct/{video}` and node `/stream/via-home/`.

## What we learned
- The gate has no key, so it reads only aud and sub (a mismatch is a 403).
- Expiry is left to streaming, whose leeway is configurable. A fixed 60 s gate check was stricter and was dropped in review.

## Open
- DASH top-level segments (`/stream/{session}/init-*.m4s`, `chunk-*.m4s`) are not on the allow-list. Story 48.24 or 48.33 must add them before AirPlay or Cast uses DASH.
