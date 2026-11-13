# Phase 3 review: the built pages beside part B's mockups

**Status: written for the lead to sign. The help pages (60.12) add their own section.**

Each page is compared at 360, 768 and 1280 px, in English and Arabic, dark (and light at 1280 px
for the hand-made pages, which the part B mockups do not draw): the built page's visual reference
(`e2e/__screenshots__/`, rendered on Linux in the CI image) beside its part B mockup
(`mockups/png/`).

## The problem pages, their index, the legal pages, the install chooser and the 404 (60.06, 60.07, 60.05)

Epic 60's Phase 3 moves these pages onto part B's shell. The `problem-p3`, `problems-p3` and
`legal-p3` mockups are part A's layouts in part B's shell (the same `<main>`, byte for byte, as
the part A mockups): the wordmark lockups, the nav with Download, Install, Help and Messages, the
page title in the display face (`.page-head h1` in `design/site.css`), and the footer's lockup in
both scripts. The build does the same: `page({ variant: "b", links })` for each of them, with
`renderProblem`, `renderIndex` and `renderLegal` unchanged, so the content stays apart from the
shell. **The install chooser and the 404 have no part B mockup: each keeps its part A layout
(the `404` mockup; the chooser's own page) inside part B's shell.** That choice is recorded here
for the lead.

### Differences

Each difference between the built pages and their mockups, fixed in the pull request or accepted.

| Where                            | Difference                                                                                                                                                                                                                    | Outcome                                                                                                                                                                                                                                                                                                                                                                                                                    |
| -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| every page: nav                  | Download goes to `/download/`, as the mockups draw it                                                                                                                                                                         | fixed: the problem family now builds after the download family (`build.mjs` `FAMILIES`), so `bLinks(resolves)` sees `/download/`; a build test checks each moved page in both languages                                                                                                                                                                                                                                    |
| every page: nav and footer, Help | Help goes to Troubleshooting (`/install/troubleshooting/`) and the footer names it, where the mockups have Help topics (`/help/`)                                                                                             | accepted until 60.12 builds `/help/`; then `bLinks` links it with no change here (as on the home page, review-phase-2)                                                                                                                                                                                                                                                                                                     |
| every page: footer               | Elsewhere keeps Security (`/.well-known/security.txt`), which the part B mockups leave out                                                                                                                                    | accepted: as on the home and download pages (review-phase-2)                                                                                                                                                                                                                                                                                                                                                               |
| every page: the current item     | Messages is marked on the problem pages and the index, Install on the chooser, nothing on the legal pages and the 404                                                                                                         | as the mockups (`currentOf`)                                                                                                                                                                                                                                                                                                                                                                                               |
| problem page                     | The title, sections and side boxes are the registry's; "Related pages" shows only when a page it names is built                                                                                                               | accepted: 60.06's rule, as in review-phase-1                                                                                                                                                                                                                                                                                                                                                                               |
| problem index                    | The reference is the first screen with the list and count masked (`FIRST_SCREEN`)                                                                                                                                             | accepted: 60.04's rule; a new registry entry changes no reference                                                                                                                                                                                                                                                                                                                                                          |
| legal: contents                  | The section being read is marked in the contents list (brass edge, the text's colour), the first at the top as the mockup draws it, and the mark follows the reader                                                           | fixed: CSS alone (`scroll-target-group: auto` on the list, `:target-current` on the link, in `src/part-b.css`), no script. The mark is a colour and the border `.toc a` already reserves, so nothing moves (CLS 0). A browser without `scroll-target-group` draws no mark, as the accepted Phase 1 row had it. There is no `aria-current="true"` in the markup: it would be wrong once the reader scrolls (review-phase-1) |
| 404                              | When the page is shorter than the window, a strip of the page's colour showed between the head's band and the footer (part B's display title is a few pixels shorter than part A's)                                           | fixed: on the 404, `<main>` is a column and its head grows to the footer (`src/part-b.css`)                                                                                                                                                                                                                                                                                                                                |
| 404                              | The fourth place is Troubleshooting, not Help; no `noindex`; the address line is written by the Worker                                                                                                                        | accepted, as in review-phase-1                                                                                                                                                                                                                                                                                                                                                                                             |
| install chooser                  | Its part A layout (a compact head, then the guides by kind) with the display-face title                                                                                                                                       | accepted: no part B mockup; the guides themselves move with 60.05's own part B layout (`install-p3`)                                                                                                                                                                                                                                                                                                                       |
| social cards                     | Every moved page has part B's card (`cardSvgB`): the both-script wordmark, the page's title in the display face, and a foot line per kind: the chooser, the problem pages, the legal pages (`cardSub` in `scripts/cards.mjs`) | fixed                                                                                                                                                                                                                                                                                                                                                                                                                      |
| install guides                   | Keep part A's shell                                                                                                                                                                                                           | accepted: out of this move; 60.05's Phase 3                                                                                                                                                                                                                                                                                                                                                                                |

### The images: problem page (the two representatives)

| Width | Language | Built (reference)                                                                                   | Mockup                                                                   |
| ----- | -------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| 360   | en       | [`problems-invalid-query-360-en.png`](../e2e/__screenshots__/problems-invalid-query-360-en.png)     | [`problem-p3.en.360.dark.png`](mockups/png/problem-p3.en.360.dark.png)   |
| 360   | ar       | [`problems-invalid-query-360-ar.png`](../e2e/__screenshots__/problems-invalid-query-360-ar.png)     | [`problem-p3.ar.360.dark.png`](mockups/png/problem-p3.ar.360.dark.png)   |
| 768   | en       | [`problems-invalid-query-768-en.png`](../e2e/__screenshots__/problems-invalid-query-768-en.png)     | [`problem-p3.en.768.dark.png`](mockups/png/problem-p3.en.768.dark.png)   |
| 768   | ar       | [`problems-invalid-query-768-ar.png`](../e2e/__screenshots__/problems-invalid-query-768-ar.png)     | [`problem-p3.ar.768.dark.png`](mockups/png/problem-p3.ar.768.dark.png)   |
| 1280  | en       | [`problems-invalid-query-1280-en.png`](../e2e/__screenshots__/problems-invalid-query-1280-en.png)   | [`problem-p3.en.1280.dark.png`](mockups/png/problem-p3.en.1280.dark.png) |
| 1280  | ar       | [`problems-invalid-query-1280-ar.png`](../e2e/__screenshots__/problems-invalid-query-1280-ar.png)   | [`problem-p3.ar.1280.dark.png`](mockups/png/problem-p3.ar.1280.dark.png) |
| 360   | en       | [`problems-node-signature-360-en.png`](../e2e/__screenshots__/problems-node-signature-360-en.png)   | [`problem-p3.en.360.dark.png`](mockups/png/problem-p3.en.360.dark.png)   |
| 360   | ar       | [`problems-node-signature-360-ar.png`](../e2e/__screenshots__/problems-node-signature-360-ar.png)   | [`problem-p3.ar.360.dark.png`](mockups/png/problem-p3.ar.360.dark.png)   |
| 768   | en       | [`problems-node-signature-768-en.png`](../e2e/__screenshots__/problems-node-signature-768-en.png)   | [`problem-p3.en.768.dark.png`](mockups/png/problem-p3.en.768.dark.png)   |
| 768   | ar       | [`problems-node-signature-768-ar.png`](../e2e/__screenshots__/problems-node-signature-768-ar.png)   | [`problem-p3.ar.768.dark.png`](mockups/png/problem-p3.ar.768.dark.png)   |
| 1280  | en       | [`problems-node-signature-1280-en.png`](../e2e/__screenshots__/problems-node-signature-1280-en.png) | [`problem-p3.en.1280.dark.png`](mockups/png/problem-p3.en.1280.dark.png) |
| 1280  | ar       | [`problems-node-signature-1280-ar.png`](../e2e/__screenshots__/problems-node-signature-1280-ar.png) | [`problem-p3.ar.1280.dark.png`](mockups/png/problem-p3.ar.1280.dark.png) |

### The images: the index

| Width      | Language | Built (reference)                                                                 | Mockup                                                                     |
| ---------- | -------- | --------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| 360        | en       | [`problems-360-en.png`](../e2e/__screenshots__/problems-360-en.png)               | [`problems-p3.en.360.dark.png`](mockups/png/problems-p3.en.360.dark.png)   |
| 360        | ar       | [`problems-360-ar.png`](../e2e/__screenshots__/problems-360-ar.png)               | [`problems-p3.ar.360.dark.png`](mockups/png/problems-p3.ar.360.dark.png)   |
| 768        | en       | [`problems-768-en.png`](../e2e/__screenshots__/problems-768-en.png)               | [`problems-p3.en.768.dark.png`](mockups/png/problems-p3.en.768.dark.png)   |
| 768        | ar       | [`problems-768-ar.png`](../e2e/__screenshots__/problems-768-ar.png)               | [`problems-p3.ar.768.dark.png`](mockups/png/problems-p3.ar.768.dark.png)   |
| 1280       | en       | [`problems-1280-en.png`](../e2e/__screenshots__/problems-1280-en.png)             | [`problems-p3.en.1280.dark.png`](mockups/png/problems-p3.en.1280.dark.png) |
| 1280       | ar       | [`problems-1280-ar.png`](../e2e/__screenshots__/problems-1280-ar.png)             | [`problems-p3.ar.1280.dark.png`](mockups/png/problems-p3.ar.1280.dark.png) |
| 1280 light | en       | [`problems-1280-en-light.png`](../e2e/__screenshots__/problems-1280-en-light.png) | none (the part B mockup is dark only)                                      |
| 1280 light | ar       | [`problems-1280-ar-light.png`](../e2e/__screenshots__/problems-1280-ar-light.png) | none (the part B mockup is dark only)                                      |

### The images: legal

| Width      | Language | Built (reference)                                                                               | Mockup                                                               |
| ---------- | -------- | ----------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| 360        | en       | [`legal-privacy-360-en.png`](../e2e/__screenshots__/legal-privacy-360-en.png)                   | [`legal-p3.en.360.dark.png`](mockups/png/legal-p3.en.360.dark.png)   |
| 360        | ar       | [`legal-privacy-360-ar.png`](../e2e/__screenshots__/legal-privacy-360-ar.png)                   | [`legal-p3.ar.360.dark.png`](mockups/png/legal-p3.ar.360.dark.png)   |
| 768        | en       | [`legal-privacy-768-en.png`](../e2e/__screenshots__/legal-privacy-768-en.png)                   | [`legal-p3.en.768.dark.png`](mockups/png/legal-p3.en.768.dark.png)   |
| 768        | ar       | [`legal-privacy-768-ar.png`](../e2e/__screenshots__/legal-privacy-768-ar.png)                   | [`legal-p3.ar.768.dark.png`](mockups/png/legal-p3.ar.768.dark.png)   |
| 1280       | en       | [`legal-privacy-1280-en.png`](../e2e/__screenshots__/legal-privacy-1280-en.png)                 | [`legal-p3.en.1280.dark.png`](mockups/png/legal-p3.en.1280.dark.png) |
| 1280       | ar       | [`legal-privacy-1280-ar.png`](../e2e/__screenshots__/legal-privacy-1280-ar.png)                 | [`legal-p3.ar.1280.dark.png`](mockups/png/legal-p3.ar.1280.dark.png) |
| 1280 light | en       | [`legal-privacy-1280-en-light.png`](../e2e/__screenshots__/legal-privacy-1280-en-light.png)     | none (the part B mockup is dark only)                                |
| 1280 light | ar       | [`legal-privacy-1280-ar-light.png`](../e2e/__screenshots__/legal-privacy-1280-ar-light.png)     | none (the part B mockup is dark only)                                |
| 360        | en       | [`legal-terms-360-en.png`](../e2e/__screenshots__/legal-terms-360-en.png)                       | [`legal-p3.en.360.dark.png`](mockups/png/legal-p3.en.360.dark.png)   |
| 360        | ar       | [`legal-terms-360-ar.png`](../e2e/__screenshots__/legal-terms-360-ar.png)                       | [`legal-p3.ar.360.dark.png`](mockups/png/legal-p3.ar.360.dark.png)   |
| 768        | en       | [`legal-terms-768-en.png`](../e2e/__screenshots__/legal-terms-768-en.png)                       | [`legal-p3.en.768.dark.png`](mockups/png/legal-p3.en.768.dark.png)   |
| 768        | ar       | [`legal-terms-768-ar.png`](../e2e/__screenshots__/legal-terms-768-ar.png)                       | [`legal-p3.ar.768.dark.png`](mockups/png/legal-p3.ar.768.dark.png)   |
| 1280       | en       | [`legal-terms-1280-en.png`](../e2e/__screenshots__/legal-terms-1280-en.png)                     | [`legal-p3.en.1280.dark.png`](mockups/png/legal-p3.en.1280.dark.png) |
| 1280       | ar       | [`legal-terms-1280-ar.png`](../e2e/__screenshots__/legal-terms-1280-ar.png)                     | [`legal-p3.ar.1280.dark.png`](mockups/png/legal-p3.ar.1280.dark.png) |
| 1280 light | en       | [`legal-terms-1280-en-light.png`](../e2e/__screenshots__/legal-terms-1280-en-light.png)         | none (the part B mockup is dark only)                                |
| 1280 light | ar       | [`legal-terms-1280-ar-light.png`](../e2e/__screenshots__/legal-terms-1280-ar-light.png)         | none (the part B mockup is dark only)                                |
| 360        | en       | [`legal-copyright-360-en.png`](../e2e/__screenshots__/legal-copyright-360-en.png)               | [`legal-p3.en.360.dark.png`](mockups/png/legal-p3.en.360.dark.png)   |
| 360        | ar       | [`legal-copyright-360-ar.png`](../e2e/__screenshots__/legal-copyright-360-ar.png)               | [`legal-p3.ar.360.dark.png`](mockups/png/legal-p3.ar.360.dark.png)   |
| 768        | en       | [`legal-copyright-768-en.png`](../e2e/__screenshots__/legal-copyright-768-en.png)               | [`legal-p3.en.768.dark.png`](mockups/png/legal-p3.en.768.dark.png)   |
| 768        | ar       | [`legal-copyright-768-ar.png`](../e2e/__screenshots__/legal-copyright-768-ar.png)               | [`legal-p3.ar.768.dark.png`](mockups/png/legal-p3.ar.768.dark.png)   |
| 1280       | en       | [`legal-copyright-1280-en.png`](../e2e/__screenshots__/legal-copyright-1280-en.png)             | [`legal-p3.en.1280.dark.png`](mockups/png/legal-p3.en.1280.dark.png) |
| 1280       | ar       | [`legal-copyright-1280-ar.png`](../e2e/__screenshots__/legal-copyright-1280-ar.png)             | [`legal-p3.ar.1280.dark.png`](mockups/png/legal-p3.ar.1280.dark.png) |
| 1280 light | en       | [`legal-copyright-1280-en-light.png`](../e2e/__screenshots__/legal-copyright-1280-en-light.png) | none (the part B mockup is dark only)                                |
| 1280 light | ar       | [`legal-copyright-1280-ar-light.png`](../e2e/__screenshots__/legal-copyright-1280-ar-light.png) | none (the part B mockup is dark only)                                |

### The images: the install chooser (no part B mockup)

| Width      | Language | Built (reference)                                                               | Mockup                                |
| ---------- | -------- | ------------------------------------------------------------------------------- | ------------------------------------- |
| 360        | en       | [`install-360-en.png`](../e2e/__screenshots__/install-360-en.png)               | none (part A's layout, below)         |
| 360        | ar       | [`install-360-ar.png`](../e2e/__screenshots__/install-360-ar.png)               | none (part A's layout, below)         |
| 768        | en       | [`install-768-en.png`](../e2e/__screenshots__/install-768-en.png)               | none (part A's layout, below)         |
| 768        | ar       | [`install-768-ar.png`](../e2e/__screenshots__/install-768-ar.png)               | none (part A's layout, below)         |
| 1280       | en       | [`install-1280-en.png`](../e2e/__screenshots__/install-1280-en.png)             | none (part A's layout, below)         |
| 1280       | ar       | [`install-1280-ar.png`](../e2e/__screenshots__/install-1280-ar.png)             | none (part A's layout, below)         |
| 1280 light | en       | [`install-1280-en-light.png`](../e2e/__screenshots__/install-1280-en-light.png) | none (the part B mockup is dark only) |
| 1280 light | ar       | [`install-1280-ar-light.png`](../e2e/__screenshots__/install-1280-ar-light.png) | none (the part B mockup is dark only) |

### The images: 404 (no part B mockup; part A's `404` mockup for its layout)

| Width      | Language | Built (reference)                                                       | Mockup                                                     |
| ---------- | -------- | ----------------------------------------------------------------------- | ---------------------------------------------------------- |
| 360        | en       | [`404-360-en.png`](../e2e/__screenshots__/404-360-en.png)               | [`404.en.360.dark.png`](mockups/png/404.en.360.dark.png)   |
| 360        | ar       | [`404-360-ar.png`](../e2e/__screenshots__/404-360-ar.png)               | [`404.ar.360.dark.png`](mockups/png/404.ar.360.dark.png)   |
| 768        | en       | [`404-768-en.png`](../e2e/__screenshots__/404-768-en.png)               | [`404.en.768.dark.png`](mockups/png/404.en.768.dark.png)   |
| 768        | ar       | [`404-768-ar.png`](../e2e/__screenshots__/404-768-ar.png)               | [`404.ar.768.dark.png`](mockups/png/404.ar.768.dark.png)   |
| 1280       | en       | [`404-1280-en.png`](../e2e/__screenshots__/404-1280-en.png)             | [`404.en.1280.dark.png`](mockups/png/404.en.1280.dark.png) |
| 1280       | ar       | [`404-1280-ar.png`](../e2e/__screenshots__/404-1280-ar.png)             | [`404.ar.1280.dark.png`](mockups/png/404.ar.1280.dark.png) |
| 1280 light | en       | [`404-1280-en-light.png`](../e2e/__screenshots__/404-1280-en-light.png) | none (the 404 mockup is dark only)                         |
| 1280 light | ar       | [`404-1280-ar-light.png`](../e2e/__screenshots__/404-1280-ar-light.png) | none (the 404 mockup is dark only)                         |

## Not claimed here

The owner's read of the Arabic copy (Q2, A5) is still open; nothing in this review stands for it.
