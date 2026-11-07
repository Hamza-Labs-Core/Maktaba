---
schema: 1
kind: pr
pr: 888
title: Epic 53 story 53.08's leftovers — a request's id on its jobs and its playlists
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [686, 839, 852]
---

## What happened
- A job queued by a request carries the request's id in its payload (`origin_request_id`, set by `queues.Enqueue`; the channel builder's `write_channels` run, which had its own copy of the statement, now goes through it too). The worker binds it on the job's lines, and the log index files a line without its own `request_id` under it, so the admin's search for one id finds the API's line and then the job's.
- Streaming takes `X-Request-Id`, or `rid` on a playlist URL (a UUID only), for its `stream_request_failed` line and its handlers' context. The web player puts the session-open call's id on the playlist as `rid`.
- **Bug found on the way:** `LogFailures` read chi's request id from `r.Context()`, but it wraps the router, and chi's `RequestID` middleware runs inside it. So no streaming failure line ever carried a request id. Its test sent no id, so it never noticed.
- Issue #830 (load-small failing about half the time) was closed without a new PR: #839 had already fixed it by comparing on one runner, and all 34 `load-small` runs since passed, within about 1 %.
- #852 (53.17) has only the Core bundle red (6 processes, 721.9 MB) and the `ci-success` umbrella that reports it. It waits for #859.

## What we learned
- A middleware that wraps a router can't read what the router's own middleware puts on the context. Test a log field with the value sent in, not only that the line exists.
- `processing_jobs_payload_merge` keeps the old payload's keys on a coalesce, so a new payload key needs no migration: a coalesced job keeps the first request's id.
- Before writing a fix for a flake issue, check that it hasn't already been fixed. #830 was fixed two hours before this session read it, and the fix PR didn't link the issue.
- mlog's context handler adds the context's ids to a record. A line that also passes the id as an attribute must log with a context that doesn't carry it, or the id is written twice.

## Open
- The native players (Apple's `StreamProxy`, Android's and the TV's) don't send the session's id on stream fetches yet.
