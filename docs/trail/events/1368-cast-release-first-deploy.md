---
schema: 1
kind: pr
pr: 1368
title: "The Cast receiver's first release works before cast.maktaba.tv exists"
date: 2026-11-15
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- The v0.3.143 tag's `Cast receiver release` workflow failed at
  `node scripts/release.mjs fetch --live-base https://cast.maktaba.tv`, with `fetch failed`.
  cast.maktaba.tv has no DNS record yet (NXDOMAIN at 1.1.1.1), because the Cast registration and
  deploy are still owner actions. The script did read a 404 as a first deploy, but Node's fetch
  throws before any status exists when the name doesn't resolve.
- #1368 merged as 1bfa78ab4. `fetchLive` now returns no previous builds only when nothing is
  deployed: the name does not resolve (`ENOTFOUND`, read from undici's `cause.code`) or the
  manifest answers 404. Every other answer still fails the release:
  - `EAI_AGAIN`, a refused, reset or timed-out connection, or a TLS error;
  - any other status;
  - a non-JSON body, or a manifest that isn't `{builds: [{level, path}]}`.
- `test/release.test.ts` has 20 cases, covering both sides. Run by hand against real hosts:
  - cast.maktaba.tv: reads no builds, exit 0;
  - a 404: reads no builds;
  - a socket error: exit 1.
- The failed v0.3.143 run wasn't re-run or dispatched; the next tag picks the fix up.

## What we learned
- A first-deploy branch that tests for a 404 never runs when the host doesn't exist at all: Node's
  fetch throws `TypeError: fetch failed` first, with the reason only in `cause.code`. A release
  step that reads a not-yet-live site needs the DNS case as well as the 404 case.
- Treat as empty only the answers that prove nothing is deployed (NXDOMAIN, 404). `EAI_AGAIN`
  and 5xx say nothing about the site, so they must fail. Otherwise a broken live site gets
  overwritten as if it were new.
- This Mac's command sandbox resolves every unknown name as `ENOTFOUND`, so `maktaba.tv` failed
  there too. Before trusting a local DNS result, check it outside the sandbox or with
  `dig @1.1.1.1`.

## Keep doing
- When a tag workflow depends on an owner action that isn't done yet, make the workflow's
  not-yet-live path reach a summary line, not an error.

## Open
- Owner: register the receiver and deploy cast.maktaba.tv (docs/stores/cast.md, Owner actions),
  then set `CAST_RECEIVER_LIVE=true`. From then on, the 404 and DNS paths stop applying and the
  live manifest must parse.
