---
schema: 1
kind: pr
pr: 1171
title: "website: the quality bar in CI (Epic 60 story 60.04)"
date: 2026-11-11
session: 04bf1f6a-ae59-4a67-8934-959a93ce4826
---

## What happened
- Epic 60 story 60.04 landed in #1171 (merge 84caba552). `_website.yml` now has three jobs:
  `website` (build, unit and script tests, a CSS lint for logical properties, the committed
  `dist-manifest.json` against the build, `links.mjs --dist dist`, the Worker smoke), `quality`
  (Playwright with axe across WCAG 2.0/2.1/2.2 A+AA, keyboard, focus ring, target size, reflow,
  text spacing, reduced motion, lang, CLS, RTL, and 70 visual references) and `lighthouse`
  (mobile, the median of 3, at least 95 in all four categories, CLS 0, 500 KB on hand-made pages).
- `website-nightly.yml` sweeps every page in 8 shards of 15 minutes. `website-deploy.yml` has a
  `nightly` job (`tools/ci/website_nightly_state.sh`): it measures a red sweep's pages again in
  the deploy's own build, and refuses to deploy when the sweep is red and its page list cannot
  be read.
- The build adds head tags, 1200×630 social cards (resvg), icons and the manifest, `sitemap.xml`
  with alternates, `robots.txt`, and JSON-LD on the home page.
- CI, all 20 pages: 100/100/100/100, CLS 0, 8–29 KB. The T02 control (an unsized late image,
  CLS 0.039) was refused. Playwright: 384 passed, 22 skipped. The menu tests skip because the
  shell declares `maktaba-menu: none`.
- Faults the gates found in the install guides, fixed in the same PR: an unlabelled output
  textarea (Docker, Synology); `<pre>` blocks that scroll but cannot be reached by keyboard
  (QNAP, TrueNAS, UGREEN, Unraid); layout shift from script-rendered content on Docker and NAS
  (now `blocking="render"`, added in a Vite `transformIndexHtml` hook because Vite drops the
  attribute); Synology was 341 px wide at 320 px with text spacing (`overflow-wrap`); and a
  physical `text-align: left`.
- CI stops hit on the way:
  - `affected-tests` wanted tests for `page.ts` and `synologyPage.ts`; real label tests were
    added.
  - CodeQL flagged a partial regex escape in `site.test.ts`.
  - `test_artifact_download.py` requires the 3-attempt retry chain on every new
    `download-artifact` step.
  - `test_ci_paths.py` requires the deploy's paths to equal the website filter exactly.
  - Two Apple jobs ran only because `ci.yml` changed (a Remote UI artifact upload failing with
    ENOTFOUND, and an iPad double-tap seek timeout); a re-run passed both.

## What we learned
- Container jobs cannot write to `runner.temp`: it is a host path. Keep logs and reports under
  the package (`logs/`, `lighthouse-out/`).
- resvg-js ignores `fontBuffers`, and its glyph fallback ignores weight. Pass `fontFiles`, and
  give the bold Latin face its own family name.
- A lockfile written on macOS can lack Linux optional packages (`@emnapi/*`), and then `npm ci`
  fails in the Playwright image. Regenerate the lock from scratch.
- The visual references must come from the same amd64 Playwright image CI runs. Run
  `npm run e2e:update` through Docker, never on the Mac.
- `spawnSync` of lhci deadlocked a fixture server in the same process. Use async spawn.
- A rebuilt `dist/` under a running `wrangler dev` serves empty pages. Restart it.

## Keep doing
- A positive control for each gate (an unsized image for Lighthouse, a routed shifting page for
  CLS, a removed focus ring) so that a gate cannot pass vacuously.
- Read the failing step's log before a re-run; re-run only infra flakes.

## Stop doing
- Adding a `download-artifact` step without its retry chain.

## Open
- `icon.svg` is not shipped: there is no vector source of the mark in the repo.
- `links-baseline.json` still has 9 entries (pages from later Epic 60 stories).
- The visual references show the placeholder look; design PR #1163 replaces them.
- The menu tests run once the shell declares a phone menu (`maktaba-menu: phone`).
