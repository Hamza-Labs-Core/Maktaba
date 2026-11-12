# Phase 1 review: the built pages beside part A's mockups

**Status: signed by the lead on 2026-11-12 (the table at the end). Written with the pull request
that put the part A shell on every page the site builds (#1192).**

Each page kind is compared at 360, 768 and 1280 px, in English and Arabic, dark: the built page's
visual reference (`e2e/__screenshots__/`, rendered on Linux in the CI image) beside its mockup
(`mockups/png/`). The hand-made pages also have a light reference at 1280 px, beside the light
mockups where the mockups have one (front door, legal). The problem page's reference is a
representative (`/problems/node-signature/`, the longest), and the index's is its first screen
with the list and count masked (`e2e/visual.spec.ts`), so the mockups' own sample entry and list
do not appear.

## Differences

Each difference between a built page and its mockup, fixed in the pull request or accepted.

| Page kind     | Difference                                                                                                                                                           | Outcome                                                                                                                                                                                                                                                                                                                  |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| all           | The footer's Help group links Troubleshooting (`/install/troubleshooting/`) where the mockup links Help topics (`/help/`)                                            | accepted: the help pages are story 60.12; `HELP` in `scripts/shell.mjs` moves to `/help/` when they ship                                                                                                                                                                                                                 |
| all           | The footer's Elsewhere group keeps a Security link (`/.well-known/security.txt`) the mockup does not draw                                                            | accepted: the placeholder footer had it, and the disclosure policy should stay one click from every page                                                                                                                                                                                                                 |
| all           | The language hint is offered only on an English page in a browser whose first language is Arabic; the design README adds the other way round                         | accepted: Epic 60 README §6.5 names that one direction, and many Arabic readers browse with an English-first browser, who would see the line on nearly every Arabic page                                                                                                                                                 |
| all           | The hint's Hide button is the quiet button at 36 px (the mockup's `sheet-small`, a mockup-only class)                                                                | fixed: `src/site.css` gives `.lang-hint .button` the same size                                                                                                                                                                                                                                                           |
| all           | Arabic pages preload all four font files (83 KB); the mockups preload three                                                                                          | fixed in the build: Latin words set in the semibold (titles, labels) swapped in late and moved their lines (layout shift on the Arabic install and problem pages)                                                                                                                                                        |
| all           | The faces are `font-display: optional` (the part A spec said `swap`)                                                                                                 | fixed: with `swap`, CI's Linux image (no Arial, so another fallback face) measured a layout shift of 0.00005 on the English pages when the preloaded face arrived after the first paint; a preloaded `optional` face is drawn from the first paint, and a late one is never swapped in (design/fonts.css and its README) |
| front door    | The ways-in band's padding was an inline style in the mockup                                                                                                         | fixed: a class (`band flush-start`), since the CSP allows no inline style                                                                                                                                                                                                                                                |
| legal         | The contents list does not mark its first entry as current (the mockup draws `aria-current` on it)                                                                   | accepted: the mark would be wrong once the reader scrolls; a scroll-aware mark is left to part B                                                                                                                                                                                                                         |
| legal         | Real copy: the date reads "Last changed 11 November 2026" (mockup: "on 11 November"), the sections and summary are the page's own (story 60.07)                      | accepted: the copy is the legal pages' own                                                                                                                                                                                                                                                                               |
| problem       | The title, sections and side boxes are the registry's (`shared/api/problems.json`); "Related pages" shows only when a help topic or install anchor it names is built | accepted: 60.06's rule; no representative has a related page until 60.12                                                                                                                                                                                                                                                 |
| problem index | The slug is a `<code>` (no chip) where the mockup has a plain span                                                                                                   | accepted: the Arabic Latin-share check counts it as code; `src/site.css` draws it as the mockup does                                                                                                                                                                                                                     |
| install guide | The guide's own `<main>` stays (it is the skip link's target) inside the frame, with its header before it, where the mockup nests the guide in a second `<main>`     | fixed: one `<main>` per page; the guide's scripts keep finding their elements                                                                                                                                                                                                                                            |
| install guide | The guide's own skip link is removed; the shell's goes to the guide's `<main>`                                                                                       | fixed                                                                                                                                                                                                                                                                                                                    |
| 404           | The fourth place is Troubleshooting, not Help                                                                                                                        | accepted: as the footer, until 60.12                                                                                                                                                                                                                                                                                     |
| 404           | The address asked for is written by the Worker (escaped, at most 200 characters); at `/404.html` itself the line is hidden                                           | fixed: no script and no layout shift                                                                                                                                                                                                                                                                                     |
| 404           | No `noindex` (the mockup has one)                                                                                                                                    | accepted: the page answers with status 404, which keeps it out of indexes, and Lighthouse's SEO audit fails a `noindex` page                                                                                                                                                                                             |

## The images

### Text page (front door)

Superseded: the home page (60.10, #1208) replaced the front door at `/` and `/ar/`, and these
references are now the home page's, reviewed in `review-phase-2.md`. The rows below compared
the front door when it was built.

| Width | Language | Built (reference)                                             | Mockup                                                                   |
| ----- | -------- | ------------------------------------------------------------- | ------------------------------------------------------------------------ |
| 360   | en       | [`home-360-en.png`](../e2e/__screenshots__/home-360-en.png)   | [`front-door.en.360.dark.png`](mockups/png/front-door.en.360.dark.png)   |
| 360   | ar       | [`home-360-ar.png`](../e2e/__screenshots__/home-360-ar.png)   | [`front-door.ar.360.dark.png`](mockups/png/front-door.ar.360.dark.png)   |
| 768   | en       | [`home-768-en.png`](../e2e/__screenshots__/home-768-en.png)   | [`front-door.en.768.dark.png`](mockups/png/front-door.en.768.dark.png)   |
| 768   | ar       | [`home-768-ar.png`](../e2e/__screenshots__/home-768-ar.png)   | [`front-door.ar.768.dark.png`](mockups/png/front-door.ar.768.dark.png)   |
| 1280  | en       | [`home-1280-en.png`](../e2e/__screenshots__/home-1280-en.png) | [`front-door.en.1280.dark.png`](mockups/png/front-door.en.1280.dark.png) |
| 1280  | ar       | [`home-1280-ar.png`](../e2e/__screenshots__/home-1280-ar.png) | [`front-door.ar.1280.dark.png`](mockups/png/front-door.ar.1280.dark.png) |

### Text page (legal)

| Width | Language | Built (reference)                                                               | Mockup                                                         |
| ----- | -------- | ------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| 360   | en       | [`legal-privacy-360-en.png`](../e2e/__screenshots__/legal-privacy-360-en.png)   | [`legal.en.360.dark.png`](mockups/png/legal.en.360.dark.png)   |
| 360   | ar       | [`legal-privacy-360-ar.png`](../e2e/__screenshots__/legal-privacy-360-ar.png)   | [`legal.ar.360.dark.png`](mockups/png/legal.ar.360.dark.png)   |
| 768   | en       | [`legal-privacy-768-en.png`](../e2e/__screenshots__/legal-privacy-768-en.png)   | [`legal.en.768.dark.png`](mockups/png/legal.en.768.dark.png)   |
| 768   | ar       | [`legal-privacy-768-ar.png`](../e2e/__screenshots__/legal-privacy-768-ar.png)   | [`legal.ar.768.dark.png`](mockups/png/legal.ar.768.dark.png)   |
| 1280  | en       | [`legal-privacy-1280-en.png`](../e2e/__screenshots__/legal-privacy-1280-en.png) | [`legal.en.1280.dark.png`](mockups/png/legal.en.1280.dark.png) |
| 1280  | ar       | [`legal-privacy-1280-ar.png`](../e2e/__screenshots__/legal-privacy-1280-ar.png) | [`legal.ar.1280.dark.png`](mockups/png/legal.ar.1280.dark.png) |

### Problem page

| Width | Language | Built (reference)                                                                                   | Mockup                                                             |
| ----- | -------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| 360   | en       | [`problems-node-signature-360-en.png`](../e2e/__screenshots__/problems-node-signature-360-en.png)   | [`problem.en.360.dark.png`](mockups/png/problem.en.360.dark.png)   |
| 360   | ar       | [`problems-node-signature-360-ar.png`](../e2e/__screenshots__/problems-node-signature-360-ar.png)   | [`problem.ar.360.dark.png`](mockups/png/problem.ar.360.dark.png)   |
| 768   | en       | [`problems-node-signature-768-en.png`](../e2e/__screenshots__/problems-node-signature-768-en.png)   | [`problem.en.768.dark.png`](mockups/png/problem.en.768.dark.png)   |
| 768   | ar       | [`problems-node-signature-768-ar.png`](../e2e/__screenshots__/problems-node-signature-768-ar.png)   | [`problem.ar.768.dark.png`](mockups/png/problem.ar.768.dark.png)   |
| 1280  | en       | [`problems-node-signature-1280-en.png`](../e2e/__screenshots__/problems-node-signature-1280-en.png) | [`problem.en.1280.dark.png`](mockups/png/problem.en.1280.dark.png) |
| 1280  | ar       | [`problems-node-signature-1280-ar.png`](../e2e/__screenshots__/problems-node-signature-1280-ar.png) | [`problem.ar.1280.dark.png`](mockups/png/problem.ar.1280.dark.png) |

### Problem index

| Width | Language | Built (reference)                                                     | Mockup                                                               |
| ----- | -------- | --------------------------------------------------------------------- | -------------------------------------------------------------------- |
| 360   | en       | [`problems-360-en.png`](../e2e/__screenshots__/problems-360-en.png)   | [`problems.en.360.dark.png`](mockups/png/problems.en.360.dark.png)   |
| 360   | ar       | [`problems-360-ar.png`](../e2e/__screenshots__/problems-360-ar.png)   | [`problems.ar.360.dark.png`](mockups/png/problems.ar.360.dark.png)   |
| 768   | en       | [`problems-768-en.png`](../e2e/__screenshots__/problems-768-en.png)   | [`problems.en.768.dark.png`](mockups/png/problems.en.768.dark.png)   |
| 768   | ar       | [`problems-768-ar.png`](../e2e/__screenshots__/problems-768-ar.png)   | [`problems.ar.768.dark.png`](mockups/png/problems.ar.768.dark.png)   |
| 1280  | en       | [`problems-1280-en.png`](../e2e/__screenshots__/problems-1280-en.png) | [`problems.en.1280.dark.png`](mockups/png/problems.en.1280.dark.png) |
| 1280  | ar       | [`problems-1280-ar.png`](../e2e/__screenshots__/problems-1280-ar.png) | [`problems.ar.1280.dark.png`](mockups/png/problems.ar.1280.dark.png) |

### Install guide in the shell

| Width | Language | Built (reference)                                                                 | Mockup                                                                         |
| ----- | -------- | --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| 360   | en       | [`install-unraid-360-en.png`](../e2e/__screenshots__/install-unraid-360-en.png)   | [`install-guide.en.360.dark.png`](mockups/png/install-guide.en.360.dark.png)   |
| 360   | ar       | [`install-unraid-360-ar.png`](../e2e/__screenshots__/install-unraid-360-ar.png)   | [`install-guide.ar.360.dark.png`](mockups/png/install-guide.ar.360.dark.png)   |
| 768   | en       | [`install-unraid-768-en.png`](../e2e/__screenshots__/install-unraid-768-en.png)   | [`install-guide.en.768.dark.png`](mockups/png/install-guide.en.768.dark.png)   |
| 768   | ar       | [`install-unraid-768-ar.png`](../e2e/__screenshots__/install-unraid-768-ar.png)   | [`install-guide.ar.768.dark.png`](mockups/png/install-guide.ar.768.dark.png)   |
| 1280  | en       | [`install-unraid-1280-en.png`](../e2e/__screenshots__/install-unraid-1280-en.png) | [`install-guide.en.1280.dark.png`](mockups/png/install-guide.en.1280.dark.png) |
| 1280  | ar       | [`install-unraid-1280-ar.png`](../e2e/__screenshots__/install-unraid-1280-ar.png) | [`install-guide.ar.1280.dark.png`](mockups/png/install-guide.ar.1280.dark.png) |

### 404

| Width | Language | Built (reference)                                           | Mockup                                                     |
| ----- | -------- | ----------------------------------------------------------- | ---------------------------------------------------------- |
| 360   | en       | [`404-360-en.png`](../e2e/__screenshots__/404-360-en.png)   | [`404.en.360.dark.png`](mockups/png/404.en.360.dark.png)   |
| 360   | ar       | [`404-360-ar.png`](../e2e/__screenshots__/404-360-ar.png)   | [`404.ar.360.dark.png`](mockups/png/404.ar.360.dark.png)   |
| 768   | en       | [`404-768-en.png`](../e2e/__screenshots__/404-768-en.png)   | [`404.en.768.dark.png`](mockups/png/404.en.768.dark.png)   |
| 768   | ar       | [`404-768-ar.png`](../e2e/__screenshots__/404-768-ar.png)   | [`404.ar.768.dark.png`](mockups/png/404.ar.768.dark.png)   |
| 1280  | en       | [`404-1280-en.png`](../e2e/__screenshots__/404-1280-en.png) | [`404.en.1280.dark.png`](mockups/png/404.en.1280.dark.png) |
| 1280  | ar       | [`404-1280-ar.png`](../e2e/__screenshots__/404-1280-ar.png) | [`404.ar.1280.dark.png`](mockups/png/404.ar.1280.dark.png) |

## Sign-off

| Date       | Reviewer | Commit    | Outcome                                                                                                                                      | Changes asked |
| ---------- | -------- | --------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ------------- |
| 2026-11-12 | the lead | 7ebffe1e4 | approved: every difference above is fixed or accepted for its stated reason; the front door's rows are superseded by the home page (Phase 2) | none          |

How it was reviewed: the pull request that built the shell compared every reference above with
its mockup (#1192). The lead looked at the front door (1280 px, English), the longest problem page
(360 px, Arabic) and the Docker guide (768 px, Arabic) beside their mockups, then read every row of
the differences table. Phase 1's other gates hold on main: no dead maktaba.tv link (the link
check's baseline is empty), Lighthouse 95+ ×4 and CLS 0 on mobile, axe clean, the RTL tests, and
the server image public (A1). Going live stays the owner's decision.
