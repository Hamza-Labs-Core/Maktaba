---
schema: 1
kind: pr
pr: 620
title: The epics wiki back on maktaba.dev in English and Arabic
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 90f3578e5. The epics wiki is on maktaba.dev in English (`/wiki/`) and Arabic (`/ar/wiki/`, RTL); deploy run 41724169451 passed with the new wiki smoke checks.
- The Arabic lives in a translation store, `docs/wiki/db/ar/` (one file per epic plus `common.json`), keyed by entry id and field with a sha256 of the English; `generate_wiki.py` writes `web/wiki-app/public/db/ar/`, and endpoint summaries reuse `openapi.ar.json`.
- All 1333 entries of Epics 1-54 are translated (1221 texts written by hand). `generate_wiki.py --check` and the devportal `checkWiki` fail on missing or stale Arabic, dropped code, or mostly-Latin prose, naming the file, entry and field; `--ar-todo` lists what to translate.

## What we learned
- The repo `.gitignore` `lib/` rule hides new files under `web/wiki-app/src/lib/`, so `i18n.ts` had to be force-added.
