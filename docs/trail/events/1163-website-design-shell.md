---
schema: 1
kind: pr
pr: 1163
title: "Epic 60 stories 60.01 part A and 60.02 part A: maktaba.tv's design shell and voice guide, approved"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1163 merged as 4dbc58f4f: the `site` token group, the self-hosted text face (IBM Plex Sans
  Arabic subset and renamed "Maktaba Text", OFL), the shell's spec (`design/README.md`), mockups
  of the text page, the problem page and index, an install guide in the shell, the 404 and a
  component sheet at 360, 768 and 1280 px in English and Arabic (light too for three), their
  renders, and the voice guide (`copy/voice.md`) in both languages.
- The owner reviewed the renders through a private review page and approved the look, the voice
  guide and the wordmark on 2026-11-11 (renders at fb0cb05a3). Three decisions are recorded in
  `design/README.md`: the font keeps the name "Maktaba Text" (the OFL reserves "Plex"; IBM's
  complete files would cost about twice the bytes); Arabic addresses the household in the plural;
  the Arabic name is «مكتبة».
- The pull request stayed open while Phase 1's pages were built on a placeholder shell, so it
  needed main merged in twice; each time the pages it now ships with changed under it.
- Five CI failures on the way, each fixed or re-run:
  - Prettier rewrote two token build scripts (`web/design-system/build/*.mjs`): the child's
    local check had covered the website folder only.
  - The link check failed on the 404 mockups: their head named the mistyped address they show
    (`/instal/synology/`) as their canonical and hreflang alternates. A 404 is not a page of its
    own; the mockups now carry `noindex` and no canonical or alternates, and the link check still
    scans the mockups.
  - `website / website` failed on a stale `dist-manifest.json`: main's build copies the shell's
    fonts (`static/fonts`) into `dist/`, and the committed manifest did not list them.
  - `unit / unit` was cancelled after 25 minutes in "Install pipeline deps", still downloading
    CUDA wheels; no test had failed. Re-run, then green.

## What we learned
- A design pull request that stays open while pages are built on a placeholder collects
  conflicts and stale generated files; merge main in and rebuild before each push.
- `pipeline`'s `uv sync --frozen --all-extras` (in `_unit.yml`, and the same in `_integration`,
  `_e2e`, `_perf-ci`, `_lint` and `coverage-map`) installs the `stt` extra, whose Linux x86_64
  `torch` comes from PyPI as the CUDA 13 build with several GB of `nvidia-*` wheels and triton.
  No pipeline test imports the real torch (the tests stand in for it), so on a cold uv cache the
  step can run past the job's limit for nothing.

## Keep doing
- Run `npm run build` in `web/packages/website` and commit `dist-manifest.json` before pushing
  any change that touches the site or merges main into it; run Prettier over all of `web`.
- A mockup's head follows the same rules as a built page: no link to an address that is no page.

## Stop doing
- Pushing after a local check that leaves out the website build's manifest check.

## Open
- CI installs CUDA torch for pipeline tests that never use it (outside Epic 60): install the
  extras the tests need without torch, transformers and accelerate, or take Linux torch from
  `https://download.pytorch.org/whl/cpu` in CI; and check that main's uv cache is saved and hit.
- The approved shell goes onto every built page in the next pull request.
