---
schema: 1
kind: pr
pr: 1148
title: "fix(api): the home's own callers get their own rate budget, keyed by the local token"
date: 2026-11-11
session: e78671d5-4566-4d62-838e-171fff32e6d2
---

## What happened
- Epic 59.12's update rehearsal (#1138, the `nas-4g` leg: a 4 GB Light home with 20,000
  videos) counted 560 HTTP 429s from the home's own processes to its API in one run, and the
  container's version checks were refused too.
- Cause: `PerUser` (600/min) runs at router level, before auth, and nothing sets the limiter's
  user id. So every program on the machine fell into one `anon-ip:127.0.0.1` bucket. Each plugin
  job mints and ends a token on the TCP API (`POST`/`DELETE /internal/plugins/invocations`, the
  pipeline's `plugins/dispatch.py`), and the launcher's `/api/system/version` and `/health`
  checks arrive through the web port's proxy (`X-Forwarded-For: 127.0.0.1`). A router test drove
  2,000 runner-shaped mints and saw 1,400 refused.
- #1148 (merged at 5bcb7d1d9) adds `mw.InternalCallers`, wired before `PerIP`/`PerUser`. A
  request with the supervisor's per-boot local token (`FromSupervisor`, constant-time) draws on
  its own bucket: 60,000/min, set with `MAKTABA_INTERNAL_RATE_PER_MIN`. A context mark makes
  both client limiters let it by.
- The key is the token, not the address: a reverse proxy's and the relay agent's clients also
  arrive over loopback and stay limited. A wrong, partial or empty token, or no token file,
  counts for nothing. No principal is attached (roles unchanged), and the sign-in route table
  ignores the mark. The container's healthcheck and `serverVersion` now send the token.
- Independent review (a separate child): nothing blocking.
  - Should-fix, done: Go's client copies custom headers to a redirect's host, so the token-bearing
    checks now follow no redirect.
  - Nits, done: `FromSupervisor` caches the token read (1 s per file), since a client's header
    would otherwise cost a file read before any limit; homeproxy drops `X-Maktaba-Local-Token`;
    tests for the login table on the production stack and for a missing or empty token file.
  - Answered: the front door's and a node's link pulls also carry the token, so they share the
    internal bucket; this is now documented. Their old budget was 600/min shared with everything.
- #1121 (clientip and node trust) had already merged; there was no conflict.

## What we learned
- A limiter that runs before auth cannot key by user: `PerUser` was a second per-address limit,
  and every loopback caller was one client.
- The home's own identity is the per-boot local token. Plugin runners never get it
  (`runnerEnvDrop`), and no proxy adds it on a client's behalf. "From loopback" names a proxy's
  clients too.
- The `affected-tests` gate counts `api/main.go`'s 65 tests: a one-line env read there needs a
  test change or a `No-Test-Change:` trailer.

## Keep doing
- Reproduce a limit with production caps and request shapes before changing it, and keep the
  client cases (proxy, relay, forged header) in the same test file as the fix.

## Stop doing
- Sending a secret header with a client that follows redirects.

## Open
- The internal bucket is shared by the pipeline, the launcher's checks and the link pulls a
  viewer causes. If a busy home ever empties it, split it by caller class (the health check
  first).
- The rehearsal's `nas-4g` leg should show no 429 from the home's own callers on the next run
  after a tag that includes 5bcb7d1d9.
