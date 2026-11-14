---
schema: 1
kind: pr
pr: 1225
title: Help on maktaba.tv, published from the app's own topics (60.12)
date: 2026-11-13
session: aa4f6da6-177c-478d-81fa-7ab103e7cac3
---

## What happened
- #1225 merged at 09ec7ab6e. `scripts/help.mjs` builds the site's help from the app's Epic 41 topics (`web/src/help/topics.ts`, the en/ar markdown, titles and summaries from the app's catalogs): 88 topic pages and 2 indexes, so the app's help and the site's cannot drift.
- The build fails on a topic missing its Arabic file, title or summary, and on a link to an unknown app route. App routes render as "In Maktaba: Settings → Rules" text with the arrow pointing the page's direction; `/help/<topic>#anchor` links resolve to the site's page in the page's language and the anchor must exist.
- `/docs` and `/ar/docs` redirect (301) to `/help/` and `/ar/help/`; the desktop Help menu opens them. Footer, 404, problem pages, home features and download cards link their topics.
- Build ordering fix: families declare the paths they always build (`withDeclared`), so a family's nav can link a page built later (the reverse of #1219's trap); `build.test.ts` checks it.
- Help topic visual references cover the first 4,000 px only (the longest topic is 32,000 px tall at 360 px).
- It conflicted with #1223 once that merged; it was rebased, re-run green and merged.

## What we learned
- Generating the public help from the app's own source, with the build refusing gaps in either language, is what keeps English and Arabic help complete and in step.
- Two website PRs touching the same shell conflict on merge; merge them in order and rebase the second rather than resolving on main.

## Open
- The owner's own read of the Arabic copy (Q2, A5).
- The lead's call on one design-review row: the help index lists all 44 topics, while the mockup left out the four native-app topics.
- Pricing (60.13) waits for Epic 50's plans.
