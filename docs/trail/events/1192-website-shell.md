---
schema: 1
kind: pr
pr: 1192
title: "maktaba.tv: the part A shell on every page the site builds"
date: 2026-11-12
session: 8f3f585b-175c-4713-ae19-d7f0b39b30fd
---

## What happened
- #1192 (merged as 2534d2e11) put story 60.01 part A's approved shell on every page maktaba.tv
  builds: the front door, the install chooser, the 404, the legal pages, every problem page and
  the index, and each install guide (wrapped in the shell's header and footer as it is). The
  header has the nav, the phone menu and the language switch; the language hint shows on an
  English page in an Arabic-first browser; the footer, skip link, callouts, code blocks with
  Copy and the step list are shell parts in `scripts/shell.mjs`.
- `shellAssets()` publishes one stylesheet (design/ tokens, fonts, shell.css, then
  `src/site.css`), the head script (`src/shell-head.js`, before the first paint), the shell
  script (`design/shell.js`), Maktaba Text and a 64 px mark, all under content-hashed names in
  `/assets/`. No inline style or script; `default-src 'self'` held. The Worker writes the
  address asked for into the 404 page.
- Every page now declares `maktaba-menu="phone"`, so the 60.04 menu tests run on every page.
  `e2e/shell.spec.ts` covers the hint, Copy with its spoken Copied, the menu without script and
  the 404's address.
- Problems hit and fixed on the way:
  - the problem index's no-JS stylesheet loaded before the shell's and lost the cascade (the
    filter showed without JavaScript): its rule is now more specific;
  - Arabic install and problem pages shifted when the Latin semibold arrived late: Arabic pages
    preload all four font files (83 KB, the budget part A set);
  - CI's first run found a layout shift of 0.00005 on 304 English tests. Linux has no Arial, so
    the metric-matched fallback became another face, and the preloaded face sometimes came
    after the first paint. The faces are now `font-display: optional`, drawn from the first
    paint and never swapped in late. Lighthouse rounded the same shift to 0.000 and passed;
  - eslint refused an unused catch binding in the head script, and CodeQL a case-sensitive
    `<script` regex in a test.
- The 122 visual references came from the quality job's own renders (`website-visual-diff-4`,
  the actual images), since Docker was not running on the Mac. Each was checked beside its
  mockup before the commit.
- Lighthouse in CI (26 pages × 3, 26 sampled × 1; all three families ran because the shell
  changed): every page met the bar. Run locally on the same set: 99 to 100 on all four, CLS 0,
  84 to 135 KB.

## What we learned
- Lighthouse's CLS of 0.000 can hide a real shift; the Playwright layout-shift test, which
  wants no entry at all, is the stricter gate.
- A metric-matched fallback only matches where its local face exists. On Linux CI it does not,
  so a preloaded `swap` face that arrives after the first paint still moves lines.
- When Docker is not up locally, the visual-diff artifact of a failing quality shard holds every
  reference the change needs, rendered in the CI image.

## Keep doing
- Comparing the built pages with the mockups at every width and in both languages before the
  first push. It caught nothing structural here, because the shell is the design's own CSS.

## Stop doing
- A watcher whose check-name pattern was not tested: the second one matched `^quality` against
  names that begin `website / quality` and ran to its 90-minute bound.

## Open
- `design/review-phase-1.md` is a DRAFT: the lead completes and signs it.
- The footer's Help link and the 404's fourth place point at Troubleshooting until the help
  pages ship (60.12 moves `HELP` to `/help/`).
- The legal contents list has no scroll-aware current mark (part B).
- The language hint is one way only (§6.5); the design README's "the other way round" is not
  built, and the review records why.
