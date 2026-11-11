---
schema: 1
kind: pr
pr: 1161
title: "Epic 60 story 60.03: maktaba.tv's package, Worker, build, CI and a deploy gated on its own token and WEBSITE_LIVE"
date: 2026-11-11
session: 583c5fed-89ea-4b84-b95b-555d6f75d1f6
agent: claude-code
---

## What happened
- #1161 merged as c7372b817: `web/packages/website` (a Worker in front of static assets,
  modelled on maktaba.dev), a build that copies the install guides to the site's root (their
  hashed files moved to `install/assets/`), the Phase 1 front door and 404 page in English and
  Arabic in a replaceable shell with a placeholder stylesheet (60.01's design replaces both),
  the `website` CI filter and job (`_website.yml`, with a `wrangler dev` smoke of `dist/` and
  the Worker's log kept as an artifact), `smoke.mjs` `--base`/`notMarker`/website targets,
  `token-check.mjs`'s `dns` piece, and `website-deploy.yml` with its steps in
  `tools/cloudflare/website-deploy.mjs`.
- The deploy reads only `CLOUDFLARE_API_TOKEN_WEBSITE` and goes live only with it,
  `CLOUDFLARE_ACCOUNT_ID` and `vars.WEBSITE_LIVE == 'true'`. The account's shared token already
  deploys maktaba.dev, so a deploy keyed on it would have published maktaba.tv on merge.
  `tools/ci/tests/test_website_deploy.py` runs the gate's own script and keeps every later step
  behind it.
- Proven on a throwaway Worker `maktaba-website-preview` on workers.dev (no routes, no custom
  domains) through the real deploy script: first deploy, preview smoke, promotion, a second
  deploy that recorded the first as previous, and a rollback by hand to it. The throwaway
  Worker was deleted afterwards; `maktaba-website` was never created.
- The first CI round failed `unit / unit`: web's root vitest picked up the website's tests,
  which read a `dist/` only `_website.yml` builds. Fixed by excluding `packages/website` in
  `web/vitest.config.ts`, as `packages/devportal` already was.

## What we learned
- `wrangler versions upload` cannot create a Worker ("You cannot upload a new version of a
  Worker that does not yet exist"), so the spec's upload-then-promote flow breaks on a first
  deploy. The script bootstraps the Worker with `wrangler deploy` on the config less its routes
  (`workers_dev = false`): it serves no name until `triggers deploy` attaches the domains after
  the preview smoke passed.
- `preview_urls = true` with `workers_dev = false` holds: wrangler 4.151 sends both as written
  and warns about that state as a supported one. The subdomain settings are applied only by
  `deploy` and `triggers deploy`, never by `versions upload`.
- `versions upload` writes `{"type":"version-upload","version_id","preview_url"}` to
  `WRANGLER_OUTPUT_FILE_PATH`; reading it beats parsing the printed lines.
- `wrangler deployments list --json` is oldest first; sort by `created_on` anyway.
- A new package under `web/packages/` with its own vitest must be excluded from web's root
  vitest (and `web/tsconfig.json`, and given eslint ignores), or the web unit job runs its tests
  without their setup.

## Keep doing
- Gate a deploy whose secrets already exist in the account on a dedicated secret and a go-live
  variable, with a test that runs the gate script itself.
- Put a deploy's steps in a script with a fake-binary test, and prove the script end to end on
  a throwaway Worker on workers.dev with a separate config, deleted afterwards.

## Stop doing
- Adding a package under `web/packages/` without checking web's root vitest, tsconfig and
  eslint configs for the exclusions devportal has.

## Open
- 60.01 part A's design replaces `scripts/shell.mjs` and `static/assets/site.css`; the language
  hint line (README §6.5) comes with it.
- 60.04 (quality run, nightly sweep and its deploy gate) and 60.08 (link check) have marked
  places in `_website.yml` and `website-deploy.yml`; 60.06 and 60.07 add their smoke checks.
- Owner action A2 (changed): the secret `CLOUDFLARE_API_TOKEN_WEBSITE` with DNS Edit on
  `maktaba.tv`, then `vars.WEBSITE_LIVE = true` to go live.
