---
schema: 1
kind: pr
pr: 598
title: maktaba.dev developer portal is live
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 9d1ab6b78. `web/packages/devportal` is a Cloudflare Worker with static assets (wrangler 4.151.0, custom domains for maktaba.dev and www, no workers.dev URL, no KV, no secrets). It serves an EN/AR landing page, the wiki at `/wiki/` built with `--base /wiki/` and `VITE_WIKI_NO_MOCKUPS=1`, Redoc over `shared/api/openapi.json`, the Epic 54 developer stories, the changelog, and a security.txt generated from `disclosure.go`.
- The build is an allow-list (`DOCS`/`FILES`/`WIKI_ALLOW` in `scripts/build.mjs`). Markdown renders with raw HTML escaped and links filtered by `isSafeUrl()` (`scripts/urls.mjs`); `scripts/scan.mjs` fails the build on internal material.
- CodeQL raised four high alerts, all root-fixed.
- `devportal-deploy.yml` deploys on push to main and by hand after its own build, using the org Cloudflare secrets; it skips when they are missing and smoke-checks `/`, security.txt and the www redirect. The first deploy passed. security.txt expired 2027-01-01 (made rolling in #602).
