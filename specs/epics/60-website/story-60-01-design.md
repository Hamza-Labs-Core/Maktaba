# Story 60.01 — Design: the visual direction, page layouts and components, as reviewed mockups

> Epic 60 · Phase 1 (part A: the shell) and Phase 2 (part B: the full direction) · Depends on: —
> · Gates: every page story (60.05–60.07 on part A; 60.10–60.13, and the Phase 3 moves, on part B)

## Why

maktaba.tv is the first thing a household sees of Maktaba, and it is judged by how it looks before
anything is read. Epic 17 gave the apps a design system and left the marketing site as a
separate effort. The site needs its own direction:
- a brand look;
- a type scale that works in Arabic as well as English, with a real Arabic face;
- imagery and iconography;
- layouts for each kind of page at phone, tablet and desktop widths in both directions.

The direction has to be decided and reviewed before pages are built on it, or each page invents
its own.

## Description

The deliverable is **a design document and high-fidelity mockups in the repo**
(`web/packages/website/design/`). Each part is reviewed by the lead before any page story builds
on it.

**Part A, the shell (Phase 1, small and first).** Enough for Phase 1's text pages to be built
right once:
- **Tokens:** the `site` group added to `web/design-system/tokens/tokens.json` (below), built to
  the site's CSS the way `tokens.css` is built.
- **Type:**
  - the text face (IBM Plex Sans Arabic, both scripts);
  - the shell's heading sizes;
  - the Arabic and Latin scales side by side, with their line heights.
- **Components:**
  - header and nav (with the phone menu);
  - language switch;
  - footer;
  - callouts;
  - code and compose blocks with copy;
  - the step list.
- **Layouts:**
  - the **text page**, for legal pages and the Phase 1 front door;
  - the **problem page** and the problem index;
  - the **404**.
- Mockups of these at 360, 768 and 1280 px, English and Arabic, dark, plus the text page in light.

**Part B, the full direction (Phase 2).**
- **Brand look.** A short written direction: what the site should feel like (a household's own
  library, calm and warm, not a tech product), what it avoids, and two or three reference
  boards described in words. The **wordmark** is proposed in Latin and in Arabic «مكتبة» beside
  the existing app icon (owner question Q3).
- **Type scale:**
  - display, heading, body, small and code sizes for each script;
  - Arabic set larger and looser than Latin at the same step, building on Epic 17's
    `type-arabic`;
  - the **display face** chosen from open-licensed faces, Amiri (the apps' display face)
    first, with the reasons (Q9);
  - subsets and weights listed, with a total font budget of 150 KB per page per language.
- **Colour:** Epic 17's brand, neutral and semantic colours where they fit, and the `site` group
  for what marketing needs more of:
  - hero and section surfaces;
  - an accent for calls to action;
  - the screenshot backdrop;
  - each in dark and light, with contrast ratios stated (text 4.5:1, large text and UI 3:1).
- **Imagery and iconography:**
  - how screenshots are framed per device;
  - how many per page;
  - the backdrop;
  - one icon style (stroke weight, corner, size grid), drawn as inline SVG;
  - which icons mirror in RTL (direction arrows, "next", breadcrumbs) and which never do (play,
    media controls, logos, platform marks, checkmarks, code).
- **Page layouts** for home, download, install, help (index and topic), problems (index and
  page) and legal, each at 360, 768 and 1280 px, LTR and RTL. Each shows:
  - the grid and measure;
  - where screenshots sit;
  - what collapses on a phone;
  - the reading order for screen readers.
- **Component specs**, one section each: anatomy, states (hover, focus, active, disabled, current
  page), sizes, RTL behaviour, and accessible name and role.
  - header and nav;
  - language switch: a link to the twin page, labelled in the other language, with `hreflang`
    and `lang` on the link;
  - footer;
  - download card per platform: icon, name, version, file size, primary button, other
    formats, requirements line;
  - step list: numbered, with a screenshot or code per step;
  - code and compose blocks with a copy button and a "Copied" announcement for screen readers.
    These stay LTR inside RTL pages;
  - callouts;
  - screenshot frames for phone, tablet, desktop window, TV and browser;
  - the problem page;
  - the legal page;
  - the 404.

**Mockups.**
- **Format:** HTML and CSS under `design/mockups/`, using the real tokens and the real
  self-hosted fonts.
- **Render:** `design/render.mjs` renders them to PNG at the three widths in both languages
  (Playwright, dark, plus light where stated).
- **In the repo:** the PNGs (each under 1 MB) and their source, so a later change diffs as text
  and its images are seen in the pull request.
- **Content:** mockups use the real copy from 60.02 where it exists, never lorem ipsum. Arabic
  mockups use Arabic copy.

**Review.**
- Each part lands in its own pull request, with the mockup images listed in the body.
- The lead reviews every image at every width in both languages and records the outcome in
  `design/README.md`: approved at the commit, or the changes asked for.
- No page story opens its pull request before the part it needs is approved.
- **Per phase:** before a phase is called done, the lead compares the built pages with the mockups,
  using the visual reference run's images (60.04) beside the mockups. The review records each
  difference as fixed or accepted, with a reason, in `design/review-phase-N.md`.

## The `site` token group

Added to `tokens.json`, next to Epic 17's groups, so the same build makes it. Proposed keys (part
A fixes the names, part B the values):

| Key | For |
|---|---|
| `site.type.display-1…3`, `site.type-arabic.display-1…3` | headlines larger than the app ever uses |
| `site.measure.text`, `site.measure.wide` | line lengths: about 70 characters Latin, about 60 Arabic |
| `site.surface.hero`, `site.surface.section`, `site.surface.shot` | page bands and the screenshot backdrop |
| `site.accent.cta`, `site.accent.cta-hover` | the primary call to action |
| `site.space.section` | vertical rhythm between page sections |

## Acceptance criteria

- **Given** part A, **then** `design/` holds the shell's written spec and mockups of the text page,
  the problem page, the problem index and the 404 at 360, 768 and 1280 px in English and Arabic,
  and `design/README.md` records the lead's approval at a commit.
- **Given** part B, **then** `design/` holds the brand direction, type scales for both scripts with
  a chosen display face, the colour extension with contrast ratios, the imagery and icon rules,
  layouts for home, download, install, help, problems and legal at all three widths in both
  directions, and a spec for every component listed. The lead's approval is recorded.
- **Given** any mockup, **then** it is rendered from HTML with the real tokens and fonts, its
  Arabic version uses Arabic copy, and no image is over 1 MB.
- **Given** a phase, **then** `design/review-phase-N.md` records the comparison of built pages with
  the mockups before the phase is done.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | script | `design/mockups/` | `render.mjs` | one PNG per mockup × width × language; none over 1 MB |
| T02 | unit | `tokens.json` | build | the `site` group in the site's CSS; the app's `tokens.css` unchanged |
| T03 | unit | colour pairs listed in the spec | contrast check | every text pair ≥ 4.5:1, large and UI ≥ 3:1, dark and light |
| T04 | lint | mockup HTML | scan | no physical `left`/`right` margins or paddings; logical properties only |
| T05 | review | part A, part B | lead review | approval recorded in `design/README.md` before the gated stories open |

## Edge cases

- **Arabic and Latin in one line** (a product name, a command): the spec sets how each run is
  isolated (`<bdi>`, `dir="ltr"` on code) so punctuation lands on the right side.
- **Long Arabic words in buttons at 360 px:** buttons wrap rather than truncate. The mockups show
  the longest real label.
- **A visitor at 200% zoom or 320 px:** the layouts reflow to one column without horizontal
  scrolling (WCAG 1.4.10).
- **High-contrast and forced-colours modes:** focus rings and borders use `CanvasText` /
  `Highlight` under `forced-colors: active`.

## Build plan

| File | Change |
|---|---|
| `web/design-system/tokens/tokens.json` | the `site` group |
| `web/packages/website/design/README.md` | the direction, the type scales, the colour extension, the imagery and icon rules, the component specs, the review records |
| `web/packages/website/design/mockups/*.html`, `*.png` | the mockups and their renders |
| `web/packages/website/design/render.mjs` | renders the mockups |
| `web/packages/website/static/fonts/` | the self-hosted subsets (OFL texts beside them) |
| `web/packages/website/design/review-phase-N.md` | one per phase |
