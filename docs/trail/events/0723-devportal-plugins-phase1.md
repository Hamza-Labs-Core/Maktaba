---
schema: 1
kind: pr
pr: 723
title: maktaba.dev, installing and managing plugins, and Phase 1 on main (Epic 54)
date: 2026-11-06
session: 08ec1b23-2735-4eb1-952d-c4e10425aeba
prs: [707, 717]
---

## What happened
- A follow-up to #707 and #717 (merged before the standing rule that a feature PR updates maktaba.dev in the same PR): a new English and Arabic page, Installing and managing plugins, and the Plugins guides' status notes brought to what is on main.

## What we learned
- The portal is built with npm in `web/packages/devportal` (`npm ci` there and in `web/wiki-app`), not pnpm: `pnpm build` fails with `ERR_MODULE_NOT_FOUND` for `marked`. A new guide is one line in `GUIDES` (`scripts/build.mjs`) and one in `src/site.test.ts`; the build itself checks the English and Arabic pairing.

## Keep doing
- Update the portal's guides in the feature's own PR, both languages, and run `npm run build`, `npm run scan` and `npm test` before pushing.
