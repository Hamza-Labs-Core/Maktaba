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

## Open
- 50.30 (the zone file, `zone.mjs`, Turnstile) and 50.31 (the Tunnel, the trusted client IP) next; 50.32 adds the Worker packages and the bundles' `maktaba-app` marker the smoke check reads.
