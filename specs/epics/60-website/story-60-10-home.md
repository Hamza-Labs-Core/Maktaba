# Story 60.10 — The home page

> Epic 60 · Phase 2 · Depends on: 60.01 part B (approved), 60.02 part B (approved copy), 60.09
> (the screenshots), 60.04 (the gates)

## Why

A household that hears of Maktaba arrives at `maktaba.tv`. In under a minute it should know:
- what Maktaba is;
- that it works in its language;
- what it will look like on its TV and phone;
- where to get it.

Phase 1's front door is only links. This page is what the design and copy work is for.

## Description

The page follows 60.01 part B's home layout and 60.02's copy. The section order below is the
default, and the approved mockups decide it.

1. **Hero.**
   - The tagline (Q1), and one sentence on what Maktaba is: your films, series and recordings, on
     every screen at home, in Arabic and English.
   - The primary button, **Download**, goes to `/download/`, preselecting the visitor's platform
     from the user agent (no request).
   - The secondary button, **How it works**, goes to an anchor below.
   - The hero screenshot: the TV home in the page's language.
2. **On every screen.** Screenshots in frames: TV, phone, tablet, computer, browser. Each has a
   one-line caption naming the app.
3. **What it does for you.** Four to six short blocks, each with an icon and two sentences, from
   what the product really ships:
   - it organises the library;
   - it makes subtitles;
   - live channels;
   - search in both languages;
   - it updates itself;
   - it runs on your own computer or NAS.

   Every claim links to the help topic that shows it (60.12; before 60.12, to the install guides
   or nothing).
4. **Arabic first-class.** A short section with a side-by-side screenshot in each language. This
   section appears on both pages.
5. **Your library stays yours.** Privacy in plain words: it runs at home, nothing leaves without
   your say, no ads, no tracking. Links to `/legal/privacy/`.
6. **Get started.** Three steps:
   1. download for the computer or NAS that holds your films;
   2. follow the setup;
   3. install the app on your TV and phone.

   Links to `/download/` and `/install/`.
7. **Footer.** Help, Download, Install, Problems, Privacy, Terms, Copyright, maktaba.dev (for
   developers), GitHub, the language switch.

**Nothing on the page is a promise:**
- every feature named is in the current release;
- apps not publicly installable are not named as available (Q5);
- the cloud is mentioned only once it is live, with a link to app.maktaba.tv.

**Performance:**
- only the hero image is preloaded;
- the page ships no script beyond the platform preselection and the language hint (under 5 KB);
- fonts are the shared subsets;
- the total transfer stays under 500 KB on mobile.

**Phase 1's front door** is replaced in place. The URL and the language switch keep their behaviour,
and the smoke check's markers are updated in the same pull request.

## Acceptance criteria

- **Given** `/` and `/ar/`, **then** the page matches the approved mockups at 360, 768 and 1280 px
  (the phase's design review records it), with the approved copy and real screenshots in its
  language.
- **Given** the page, **then** it meets 60.04's bar (Lighthouse ≥ 95 × 4 on mobile, CLS 0, axe
  clean, mirrored correctly) and transfers under 500 KB on mobile.
- **Given** every feature claim, **then** the feature is in the current release, and the claim
  links to where it is explained.
- **Given** a visitor on Windows, macOS, Linux, iOS or Android, **then** Download goes to
  `/download/` with that platform first.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | visual | `/`, `/ar/` × 3 widths | compare | references (from the approved mockups' review) |
| T02 | Lighthouse | both | run | ≥ 95 × 4, CLS 0, ≤ 500 KB |
| T03 | Playwright | user agents for 5 platforms | click Download | `/download/#<platform>` |
| T04 | unit | the page's feature list | compare with `docs/wiki`'s shipped epics list and the help topics | every claim maps to a shipped feature and a link |
| T05 | smoke | live | `/` | the home page's marker |

## Edge cases

- **A user agent that hides the platform:** Download goes to `/download/` with no preselection.
- **No JavaScript:** everything shows; only the preselection is missing.
- **A feature removed in a release** (the Plex stages are removal candidates): T04 fails until its
  claim goes.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/src/pages/index.html`, `ar/index.html` | the page |
| `web/packages/website/src/home.ts` | platform preselection (tiny) |
| `web/packages/website/copy/home.{en,ar}.md` | from 60.02 |
| `tools/cloudflare/smoke.mjs` | the home page marker |
| `e2e/__screenshots__/home-*` | references |
