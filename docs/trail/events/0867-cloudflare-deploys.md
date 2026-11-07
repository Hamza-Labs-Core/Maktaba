---
schema: 1
kind: pr
pr: 867
title: Cloudflare deploys from CI only (Epic 50, 50.34)
date: 2026-11-07
session: 13624713-ebc0-4816-aea9-815781ec4f0d
prs: [723]
---

## What happened
- The cloud's Cloudflare pieces got their own workflows on devportal-deploy.yml's pattern: `ci.yml`'s `cloudflare` job checks the files offline on pull requests; `cloudflare-zone.yml` (zone rules, Tunnel ingress), `cloud-web-deploy.yml` (the two web Workers, once 50.32 adds their packages) and the weekly `cloudflare-ips.yml` deploy or propose from main only.
- `tools/cloudflare/` (Node 22, no dependency): `token-check.mjs` names a missing permission before any write, `smoke.mjs` checks the live hosts, `ips.mjs` checks Cloudflare's ranges; all tested against a local fake.

## What we learned
- A Cloudflare token can only list its own permissions when it has API Tokens: Read; without it, a read probe per permission is the next best check, and the tool should say which it used.
- Node's fetch drops a custom `Host` header, so a fake standing in for several hosts needs one server per host (or a base URL per host, as `smoke.mjs`'s `SMOKE_BASE_<HOST>` does).
- With per-piece tokens, check each piece with its own token: one check of the union with one token proves nothing about the other.
- A deploy workflow for files a later story adds (the Workers' packages) can land first: gate each piece on its file existing, with a notice, so main stays green.

## Problems on the way
- CI's first run failed: `node --test tools/cloudflare/` ("Cannot find module"). Node 22 doesn't take a folder; it takes a quoted glob, `node --test 'tools/cloudflare/*.test.mjs'` (fixed in the workflow and the README).
- CodeQL flagged twice. First, a `mask()` helper that logged part of the account id: removed, and the workflows mask the id with `::add-mask::`. Second, the test fake picking a handler by a request-supplied key, which was still flagged after the switch to a Map; it now compares against each route in a list. Lesson: a fake server should never index a table with request data.
- The merge was blocked by a check this PR doesn't touch: `mac-app / Bundle size (Core payload)` has been red on main since the merge of PR 833 (6 processes and 707 MB against Core's 4 and 700 MB). A pull request that changes `ci.yml` runs every job, so it inherits main's red ones. The fix is PR 859; the fail-closed watcher correctly refused to merge. Lesson: check main's state for a failed job before debugging it in the pull request.

## Open
- 50.30 (the zone file, `zone.mjs`, Turnstile) and 50.31 (the Tunnel, the trusted client IP) next; 50.32 adds the Worker packages and the bundles' `maktaba-app` marker the smoke check reads.
