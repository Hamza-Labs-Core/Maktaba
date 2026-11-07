---
schema: 1
kind: pr
pr: 861
title: Paid plugins' packages by signed URL only (Epic 54, story 54.19 §6)
date: 2026-11-07
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
---

## What happened
- A priced plugin's packages go only to the store's private bucket. The catalog names `plugins-cdn.maktaba.tv/paid/<sha256>`.
- A home gets a download URL (`POST /v1/homes/{id}/downloads`, signed by the home) only while it holds a valid entitlement. The URL carries an HMAC under `PAID_URL_KEY` of the home, the sha256 and a 10-minute expiry.
- `store-web` serves `/paid/*` on the CDN's host through a zone route and checks the token, the path and `X-Maktaba-Home`.
- The home installs a paid plugin through `storelink.PaidPackage`, which refuses any URL that isn't the CDN's own `/paid/<sha256>`.

## What we learned
- The spec named a separate `store-cdn` Worker. A zone route on the CDN's host, `plugins-cdn.maktaba.tv/paid/*`, served by `store-web`, does the same job with one secret in one Worker and no new deploy. The rest of the host stays the public bucket's custom domain.
- A feature that moves packages behind an entitlement changes the user's words. #850's note said a paid plugin "installs now and waits", which was true only until this PR. Read the earlier PR's strings when the later one changes the flow.
- One validity rule, one function. The store's `holds()` uses the entitlement route's own rules (paid or disputed, not ended, a yearly within its term), so a download and an entitlement never disagree.

- The store has three TypeScript configs, and CI's `npm run typecheck` runs all of them. `tsc -p .` alone passed with `new TextDecoder("utf-8", { fatal: true })`, which the Workers types refuse. Strict decoding stays without the option: check the part's base64url alphabet, decode, then require an ASCII-only payload pattern. An invalid byte becomes U+FFFD and fails the pattern.

## Keep doing
- A test case for each way a signed URL can be wrong (expired, forged, another home, another file, none), not just the happy path.

## Stop doing
- Typechecking the store with one config. Run `npm run typecheck` in `store/`.
- Telling users about behaviour the next PR in the same series is about to change.

## Open
- Moving a purchase to another home. Buy on plugins.maktaba.tv itself.
- The owner sets `PAID_URL_KEY` before paid plugins go live.
