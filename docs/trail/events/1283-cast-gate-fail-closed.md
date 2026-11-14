---
schema: 1
kind: pr
pr: 1283
title: "The cast-grant gate refuses a body it cannot read"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1283 merged as 864f21b62. `api/internal/castgrant/gate.go` (story 58.07) let a stream-session
  body it could not parse through to the handler, relying on the handler's decoder to refuse
  it. The gate now refuses such a body itself with 403 `cast_scope`.
  `TestPGCastGrantVideo` covers trailing bytes, a second JSON value and a body that isn't JSON.
- The background security review of a 50.26 branch flagged it; it came from main, not the
  branch, so it went in its own PR off main.

## What we learned
- A gate that checks a body field must decide on what it parsed, never on another
  decoder's behaviour later: two parsers can disagree on trailing data or repeated values.
- The affected-tests gate pairs a source file with its own package's tests. A change
  covered only by an end-to-end test elsewhere needs a `No-Test-Change` trailer naming that test.
- An integration job that ran over its wall budget on an older base (5m) passes once main's
  newer budget (6m30s, #1295) is in; check main's Makefile before chasing a wall overrun.

## Keep doing
- When a review flags code that came in from main, fix it in a small PR off main, not in
  the story branch.

## Open
- Nothing.
