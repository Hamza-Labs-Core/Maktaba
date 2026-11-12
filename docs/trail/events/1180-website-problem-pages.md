---
schema: 1
kind: pr
pr: 1180
title: "website: the problem pages and index, and an empty link baseline (Epic 60 story 60.06)"
date: 2026-11-11
session: db6669d4-245a-495e-abc5-e0dbeac6b13d
---

## What happened
- #1180 (merge 044cc2684) built the site half of story 60.06 on the placeholder shell:
  - `web/packages/website/scripts/problems.mjs` writes `/problems/<slug>/` and
    `/ar/problems/<slug>/` for the 288 entries of `shared/api/problems.json`, and the index
    `/problems/` grouped by `area` (578 pages). The registry becomes a structured document per
    page, which `renderProblem` and `renderIndex` draw in the sections of #1163's problem
    mockups (the way back, title, who sees it, what this means, what you can do, who can help,
    related pages, details for developers). The design swaps those two functions.
  - The build fails naming the entry and the field on a missing text or an unknown area or
    audience (T10). The Arabic pages pass the Latin-share check (highest 13%); snake_case names
    and hyphenated slugs in a text are set in `<code>` (T09).
  - The index's filter (`src/problems-filter.ts`, a content-hashed module) narrows in the page
    with no request (T11), folds Arabic short vowels and alef forms, and writes the count in
    Arabic's plural forms.
  - The Worker's redirects moved to `src/redirects.json`, which `src/worker.ts` serves and
    `scripts/links.mjs` reads: a link to `/docs` passes when its target opens. The link baseline
    is now empty (the last 9 entries: 8 `/problems/` links and `/docs`).
  - The 60.06 strings table now carries the plural address and «مكتبة»; 60.07's build plan row
    names `copy/legal/` and `renderLegal`; the problem-types guide says each entry has its page.
- About 590 pages made the 60.04 gates too slow as they were. The quality job now runs in four
  Playwright shards (9,261 tests, about 7 minutes each). A pull request that changes a family
  larger than 16 pages measures 16 of them spread evenly, plus the representatives. The nightly
  Lighthouse sweep moved from eight shards to sixteen: 650 URLs at about 11.5 s a run would
  overrun 15-minute shards.
- Lighthouse on the representatives, the index and the 16-page sample: 100 in all four
  categories, CLS 0, 8 to 20 KB a page.

## What we learned
- The first index rendering revealed its filter from the script, which shifted the layout (the
  CLS test caught it). The box is now in place from the first paint, and a `<noscript>`
  stylesheet in the head hides it without JavaScript (the CSP allows no inline style).
- A file name like `problems-noscript.css` matches the site's content-hash pattern
  (`-` plus 8 or more characters) and would be cached as immutable for a year. Check a new
  static file's name against `HASHED` in `src/headers.ts`.
- With `updateSnapshots: "none"`, a missing reference fails in CI without writing an image to
  the report, so CI cannot hand back new references. They come from `scripts/e2e-update.sh`
  (Docker, the same image).
- A full-page reference of a list built from the registry changes with every new problem type,
  and the index's weighed 1.4 to 1.9 MB against the 1 MB limit. Its reference is now the first
  screen with the list and the count masked (`FIRST_SCREEN` in `e2e/visual.spec.ts`).
- `reference-images.yml` wants every changed image path on its own line; brace globs are not
  read. Re-runs of its earlier failed runs read the body fresh, but they share one concurrency
  group, so re-run them one at a time.
- The affected-tests gate needs a test change for each changed source file: `headers.ts` and
  `worker.ts` needed a `worker.test.ts` case.

## Keep doing
- Content kept apart from one render function per page kind (legal, problems): the design
  swap stays a template and CSS change.
- When a family grows, look at the gate's runtime (tests per page × pages, Lighthouse runs ×
  11.5 s) before pushing.

## Stop doing
- Listing reference images with brace globs in a pull request body.

## Open
- The registry's own Arabic texts address the reader in the singular (لا تملك، اطلب); the page
  labels use the plural of the 60.02 voice. Moving the 288 entries' Arabic to the plural is a
  registry change (and the API's embedded copy).
- No help-link representative yet: no help topic resolves until 60.12 builds them. 60.12 adds a
  `help-link` problem representative, and its family must run before `problems` in `FAMILIES`.
- The visual design (#1163) still awaits approval; the problem pages render on the placeholder
  shell.
