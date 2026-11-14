---
schema: 1
kind: pr
pr: 1247
title: "Epic 58 story 58.08, part 2: the big screen's TV build at /tv/"
date: 2026-11-14
session: c5bd0950-9d71-4da4-acdd-7062c20f3fd6
agent: claude-code
---

## What happened
- #1247 merged as 7ddd7909a. `pnpm build:tv` builds the big screen for the Chromium 68 of a 2020
  Samsung or LG TV (plugin-legacy's modern polyfills, Lightning CSS for Chrome 68, and
  `web/tv-build/tvcss.mjs` lowering clamp/min/max, flex gap, logical properties mirrored under
  `[dir]`, aspect-ratio with a fallback, WOFF2 fonts). `web/tv-build/tvcheck.mjs` (es-check
  es2017 and a CSS lint) fails the build on what M68 cannot run.
- The home serves it at `/tv/` (page no-cache, hashed files immutable, client routes falling back
  to the TV page, a missing asset a 404); `make build-web` builds it; the web app's service worker
  leaves `/tv/` alone (it would have cached the TV page as the web shell).
- TV mode: Tizen and webOS key codes, the launcher's postMessage bridge (the seam for 58.09; key
  actions only, only from the parent, only from a packaged app's origin or the home), Leave
  Maktaba? on Back from Home, a signed-out page to the home's sign-in and back.
- `_tv-web.yml`: Vitest, build:tv with its check, Playwright on `/tv/` against a mock home (D-pad
  only, 1920×1080, dark, en and ar; CPU ×6 timing), ending with the shared tv-logs step.
- CI caught three things after the first push: the tools first lived in `tools/web/`, which the
  web and cloud images (they copy `web/` alone) could not import from `vite.config.ts`; the
  58.16 rule that every TV job ends with `.github/actions/tv-logs`; and `win-app`, whose
  `make build-web` now runs build:tv, where `node_modules/.bin/es-check` is a `.cmd` shim
  execFile cannot start.

## What we learned
- Anything `web/vite.config.ts` imports must live under `web/`: the Docker builds copy only it.
- On Windows, run a package's CLI as `node <its bin script>` (resolve it from its package.json),
  never `node_modules/.bin/<name>`; pass a globbing CLI forward-slash paths.
- A `<bdi>` (dir=auto) that is a block or flex item aligns by its own direction: an English title
  on an Arabic screen sat on the left. `[dir="rtl"] bdi:not([dir="ltr"]) { text-align: right }`.
- Main moves under a stacked pair: each merge needed `tools/dev/merge-main.sh` again; it kept
  both CHANGELOG sides each time.

## Keep doing
- Reading the Playwright screenshots in both languages before calling RTL done: the bdi
  alignment showed only there.

## Stop doing
- Adding a build step to `make build-web` without thinking of the Windows and Docker builds that
  run it.

## Open
- `frame-ancestors` for `/tv/` belongs with 58.09, once the launcher's origin is known (the web
  port sends none today; the session cookie is SameSite=Lax).
- Playback through the launcher's bridge (58.09–58.11); in a browser `/tv/` uses hls.js.
- 58.01's api_level check belongs to the launcher; it had not landed.
- Long grids page 48 at a time rather than virtualize; the real-device budget is 58.16's checklist.
- The spec's build plan names `tools/web/`; the tools live in `web/tv-build/`.
