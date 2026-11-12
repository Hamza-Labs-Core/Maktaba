# Story 60.04 — The quality bar in CI: Lighthouse, WCAG 2.2 AA, no layout shift, visual references, SEO and social cards

> Epic 60 · Phase 1 · Depends on: 60.03 · Needed by: every page story (each lands under these
> gates)

## Why

A site that is good on the day it is designed drifts the day after:
- a heavier image;
- a heading out of order;
- a button that loses its focus ring;
- an Arabic page whose arrows point the wrong way.

The quality bar in the epic's Acceptance only holds if CI measures it on every change, and if a
change to how a page looks is seen by a person before it merges.

## Description

**Lighthouse** (`@lhci/cli`, pinned):
- **Target:** `dist/`, served by `wrangler dev`, so the Worker's headers count.
- **Settings:** mobile emulation, Lighthouse's default throttling, three runs per URL, the median
  kept.
- **Assertions:** `categories:performance`, `accessibility`, `best-practices` and `seo` each
  ≥ 0.95; `cumulative-layout-shift` = 0; `total-byte-weight` ≤ 500 KB on hand-made pages.
- **Pull requests:** every hand-made page plus the representatives of each generated family, in
  both languages. Representatives are listed in `e2e/representatives.json`:
  - the longest problem page;
  - the shortest;
  - one with a help link;
  - for help (from 60.12): the longest topic, the shortest, one with a table.
- **A pull request that changes a generator or a template:** the family it renders, one run per
  URL: all of it up to 16 pages, and beyond that 16 pages spread evenly over it in both languages
  (the problem pages are one template over every registry entry; the nightly measures them all).
- **The deploy run:** the same set as pull requests. It refuses to run while the latest nightly
  sweep is red.
- **The nightly sweep on main:** every page in both languages (about 650 URLs), one run per URL,
  in sixteen shards, each with a 15-minute timeout and the whole within 60 minutes. A failure makes
  the nightly red, names the pages, and blocks the next deploy until a fix lands. Main stays
  deployable by fixing forward or reverting the change that caused it.
- **Kept:** the reports (HTML and JSON), uploaded as `website-lighthouse`, pass or fail.

**Accessibility** (Playwright + `@axe-core/playwright` on the existing `axe-core`):
- axe with the `wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa` and `wcag22aa` tags, on every page in
  both languages, dark and light;
- zero violations;
- **tested directly, beyond axe:**
  - **keyboard:** Tab reaches every link and control in reading order, the phone menu opens and
    closes with Enter and Escape, focus returns to its button, and a skip link goes to the main
    content;
  - **focus visible:** each focused control has an outline of at least 2 px with a 3:1 contrast
    (2.4.7, 2.4.11 not obscured by the sticky header);
  - **target size:** interactive targets at least 24×24 CSS px (2.5.8);
  - **reflow:** at 320 px no horizontal scroll (1.4.10); text spacing overrides do not clip
    (1.4.12);
  - **reduced motion:** with `prefers-reduced-motion: reduce`, no transition runs;
  - **language:** `lang` on `<html>`, and on each run in the other language (the language
    switch, a Latin product name in Arabic text where needed).

**Layout shift.**
- Every image has `width` and `height`, and fonts load with metric-matched fallbacks
  (`size-adjust`, `ascent-override`).
- The Lighthouse CLS assertion is 0.
- A Playwright test also records `layout-shift` entries through load and the first scroll, and
  fails on any.

**RTL mirroring** (a test, not only a picture):
- **On each Arabic page:** `dir="rtl"`; the computed `direction` of `main` is `rtl`; elements
  marked `data-mirror` have a mirrored transform or a mirrored glyph.
- **Elements marked `data-nomirror` keep their transform:** logos, platform marks, play and media
  controls, screenshots, code blocks, checkmarks.
- A CSS lint refuses `left`, `right`, `margin-left`, `padding-right`, `text-align: left` and
  similar physical properties in the site's stylesheets.

**Visual references** (Playwright `toHaveScreenshot`):
- **Pages:**
  - every hand-made page;
  - every install guide;
  - the problem and help templates with their representatives;
  - the 404.
- **At 360, 768 and 1280 px,** English and Arabic, dark. Also each hand-made page in light at
  1280.
- **Full-page screenshots,** animations off, the date frozen, and fonts loaded before the shot
  (`document.fonts.ready`).
- **Comparison:** `maxDiffPixelRatio: 0.001`, pixels not bytes.
- **References** in `web/packages/website/e2e/__screenshots__/<page>-<width>-<lang>[-light].png`,
  rendered on the CI runner's image, each under 1 MB, PNG with no metadata.
- **On a mismatch:** the job fails and uploads the expected, actual and diff images
  (`website-visual-diff`).

**A reference change needs review.**
- **Updating references:** `pnpm -C web/packages/website e2e:update` on Linux (a container of the
  runner's image, so references never come from a Mac), committed in the pull request.
- **A `Reference images:` rule.** When the diff touches `e2e/__screenshots__/` (or
  `static/shots/`, 60.09), the body must have a `Reference images:` section listing every changed
  file with one line on why. Otherwise the check fails, naming the unlisted files.
  - The rule reads the file list (`pulls.listFiles`, paginated) and the body fresh from the API
    (`pulls.get`). Today `_pr-body-check.yml` reads the body from the event payload, so a re-run
    replays the old body.
  - It runs from a small workflow on `pull_request` with types `opened`, `edited`,
    `synchronize` and `reopened`. `ci.yml` uses the default types, which leave out `edited`, so
    fixing the body would otherwise never re-run the check.
- **The reviewing session** opens every changed reference image, in both languages, before
  merging (the rule of #893), and says so in its review.

**SEO and social:**
- **Every page:**
  - `<title>` and `<meta name="description">` from the page's copy (60.02);
  - `<link rel="canonical">`;
  - `<link rel="alternate" hreflang="en|ar|x-default">`.
- **Social cards:**
  - Open Graph (`og:title`, `og:description`, `og:image`, `og:locale` `en_US` / `ar_AR`,
    `og:locale:alternate`) and `twitter:card=summary_large_image`;
  - the card image rendered at build time per page and language (1200×630, dark, the page's
    title in its script with the display face, the wordmark), as PNG under 200 KB;
  - generated families get one card per page from the same template.
- **Icons:** `favicon.ico` (32), `icon.svg`, `apple-touch-icon.png`, and a `manifest.webmanifest`
  with the 192, 512 and maskable icons, from `web/public/icons/`.
- **`sitemap.xml`:** every page with its `xhtml:link` alternates; `robots.txt` allows everything
  and names the sitemap.
- **Structured data:** `Organization` on the home page and `SoftwareApplication` on the download
  page (JSON-LD).

## Acceptance criteria

- **Given** a pull request that changes the site, **then** Lighthouse on mobile scores ≥ 95 in all
  four categories with CLS 0 for every hand-made page and representative in both languages, or
  the job fails.
- **Given** the nightly sweep, **then** every page in both languages meets the same bar, and a red
  sweep blocks the next deploy.
- **Given** any page in either language, dark or light, **then** axe reports no WCAG 2.2 A or AA
  violation, and the keyboard, focus, target-size, reflow and reduced-motion tests pass.
- **Given** an Arabic page, **then** layout and `data-mirror` elements mirror and `data-nomirror`
  elements do not.
- **Given** a change in any page's look, **then** the visual job fails until the references are
  updated, and the pull request body check fails until each changed reference is listed.
- **Given** any page, **then** it has its title, description, canonical, `hreflang` alternates,
  Open Graph and Twitter tags with a card image in its language, and appears in `sitemap.xml`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Lighthouse | representatives | run | ≥ 0.95 × 4, CLS 0 |
| T02 | Lighthouse | a page with an unsized image (fixture) | run | CLS > 0 → job fails (positive control) |
| T03 | Playwright + axe | every page × 2 languages × 2 themes | scan | zero violations |
| T04 | Playwright | keyboard | Tab through the header, menu, page, footer | order and focus as specified; Escape closes the menu |
| T05 | Playwright | 320 px | load every page | `scrollWidth ≤ clientWidth` |
| T06 | Playwright | Arabic pages | check mirror markers | `data-mirror` mirrored, `data-nomirror` not |
| T07 | lint | site CSS with `margin-left` (fixture) | run | refused |
| T08 | Playwright visual | every page × 3 widths × 2 languages | compare | matches references |
| T09 | `tools/ci` | PR body without `Reference images:` while a reference changed | the rule | fails, naming the file |
| T12 | workflow | the body edited to add the section | `edited` event | the rule re-runs on the new body and passes |
| T10 | build | every page | parse head | title, description, canonical, alternates, OG, Twitter, card file exists |
| T11 | build | `sitemap.xml` | parse | one `<url>` per page with both alternates |

## Edge cases

- **A font that renders a pixel differently after a runner image update:** the references are
  updated in their own pull request, listed and reviewed like any other change. The runner image
  is pinned (`ubuntu-24.04`), so this happens only on purpose.
- **Lighthouse noise on a shared runner:** three runs and the median on the sampled set. The
  nightly sweep's single runs re-measure a failing URL three times before calling it a failure. A page that scores 94 on one
  run and 97 on others is judged on the median. A page whose median is under 95 fails; there is no
  retry.
- **A generated family that grows** (a new problem type): it is covered by the structural, axe and
  Latin-share tests on every entry. Lighthouse covers it on deploy, and the visual references on
  its representatives.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/lighthouserc.json`, `e2e/representatives.json` | Lighthouse settings and assertions |
| `web/packages/website/e2e/{a11y,keyboard,reflow,rtl,cls,visual,head}.spec.ts` | the tests |
| `web/packages/website/e2e/__screenshots__/` | references |
| `web/packages/website/scripts/{cards,sitemap,icons}.mjs` | social cards, sitemap, icons |
| `web/packages/website/scripts/lint-css.mjs` | the logical-properties lint |
| `.github/workflows/_website.yml` | the quality jobs and artifacts |
| `.github/workflows/reference-images.yml` (new, `pull_request` with `edited`), `tools/ci/reference_images.py` and its test | the `Reference images:` rule, reading the body and files from the API |
| `.github/workflows/website-nightly.yml` | the full Lighthouse sweep |
