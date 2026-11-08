---
schema: 1
kind: pr
pr: 997
title: A fingerprint a Windows runner refuses is answered, and the worker takes it (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [986, 990, 1003]
---

## What happened
- Split out of #986 so it could land on its own. Since #990 every Windows runner's broker shim refuses a share path it is not brokered for. When an input fingerprint hit that refusal in the fingerprint runner (thumb_small's sidecar look on a share), `_fingerprints` let the `PermissionError` escape: the connection closed with no answer and no log line.
- The worker's `fingerprint_fn` fell back to the in-process fingerprint on a refusal answer, but not on the `asyncio.IncompleteReadError` a silently closed connection gives. The job failed as retryable, went back to `pending`, and looped.
- Runner: a failed fingerprint is answered `{"type":"error","status":"sandbox_refused"|"failed"}` and logged as `plugin_runner_fingerprint_failed`. Worker: `IncompleteReadError` also means the worker computes the fingerprint itself.
- Homes were spared: #990's guard keeps share-library stages, and their fingerprints, in the worker. #986's end to end sets `MAKTABA_WINDOWS_SHARE_RUNNERS=1`, which is how it surfaced (run 42463156201).
- It waited on #1003: github.com release downloads answered 504 for minutes at a time and kept its Windows jobs red until the downloads went API-first with retries and a cache.

## What we learned
- A server loop that lets one request's exception escape turns an error into an absence on the wire. Every per-request handler in the runner answers, whatever it raises.
- A caller that falls back on a refusal must treat "the peer closed without answering" as one too, or a crash in the peer becomes a retry loop instead of a fallback.

## Keep doing
- Splitting a fix out of a long-running end-to-end PR as soon as it stands alone, so it merges fail-closed on its own checks.

## Open
- Nothing for this fix; the runner-path root cause and the guard's removal are in #986's event.
