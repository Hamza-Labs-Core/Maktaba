---
schema: 1
kind: pr
pr: 1183
title: "maktaba.tv deploys with the shared Cloudflare token; WEBSITE_LIVE is the only switch; the token check runs before go-live"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- Owner action A2 was decided: no separate website token. #1183 merged as ca22c1b88.
  `website-deploy.yml` reads `CLOUDFLARE_API_TOKEN_WEBSITE` when set and the shared
  `CLOUDFLARE_API_TOKEN` otherwise. `vars.WEBSITE_LIVE` (unset) stays the only go-live switch:
  every step that writes needs it, and `tools/ci/tests/test_website_deploy.py` fails if one does
  not. The token check only reads (a test checks every Cloudflare call in `token-check.mjs` is a
  GET), so it now runs on every run once a token and the account are set; while the site is not
  live a failure is an annotation and a summary line, not a red run.
- `deploy/cloudflare/README.md` (the website's row, the token section, step 4), Epic 60's §6.3
  and A2, story 60.03 and the website guides (en, ar) say the same.
- The first deploy run on main after the merge (run 43148096477, job 130957138706) deployed
  nothing (`WEBSITE_LIVE` unset) and ran the token check with the shared token: the token is
  active; Workers Scripts Write, Workers Routes Write and Zone Read are present; **DNS Write on
  `maktaba.tv` is missing**. The token cannot read its own policy, so each permission was probed
  by one read call; the DNS probe could not even list the zone's records.
- On the way: #1183 went conflicting when #1184 merged (both edited §12's A1 and A2 rows); main
  was merged in keeping both. Its `website / lighthouse` failed twice on Lighthouse's own
  `NO_NAVSTART`, with no page under the bar; #1188 made the gate measure such a run again, and
  #1183 merged after main brought that in.
- The deploy run for #1163's merge (4dbc58f4f) shows as cancelled: GitHub keeps one queued run
  per concurrency group, and #1183's push replaced it while the run before was still going. The
  workflow never cancels a running deploy; the newer run built the newer main.

## What we learned
- A read-only check can run long before go-live and turn a go-live surprise into a known gap.
- A "cancelled" run in a never-cancelling concurrency group is a queued run replaced by a newer
  one, not a stopped deploy.

## Keep doing
- Keep every write behind the one switch, and test that it is so.

## Stop doing
- Treating a missing secret as the only safeguard when a shared secret already exists.

## Open
- **Owner:** add DNS Edit on the `maktaba.tv` zone to the shared `CLOUDFLARE_API_TOKEN` (custom
  domains write their records). Optionally add API Tokens: Read, so the check reads the token's
  policy instead of probing.
- Go-live (`WEBSITE_LIVE = true`) stays the owner's call.
