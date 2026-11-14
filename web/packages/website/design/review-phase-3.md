# Phase 3 review: the built pages beside part B's mockups

**Status: written for the lead to sign: the pages Phase 3 moved onto part B's shell (#1223), and
the help pages (60.12, #1225).**

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
| every page: nav and footer, Help | Help goes to Troubleshooting (`/install/troubleshooting/`) and the footer names it, where the mockups have Help topics (`/help/`)                                                                                             | fixed by 60.12 (#1225): with `/help/` built, `bLinks` sends Help there and the footer names Help topics, as the mockups do                                                                                                                                                                                                                                                                                                 |
| every page: footer               | Elsewhere keeps Security (`/.well-known/security.txt`), which the part B mockups leave out                                                                                                                                    | accepted: as on the home and download pages (review-phase-2)                                                                                                                                                                                                                                                                                                                                                               |
| every page: the current item     | Messages is marked on the problem pages and the index, Install on the chooser, nothing on the legal pages and the 404                                                                                                         | as the mockups (`currentOf`)                                                                                                                                                                                                                                                                                                                                                                                               |
| problem page                     | The title, sections and side boxes are the registry's; "Related pages" shows only when a page it names is built                                                                                                               | accepted: 60.06's rule, as in review-phase-1                                                                                                                                                                                                                                                                                                                                                                               |
| problem index                    | The reference is the first screen with the list and count masked (`FIRST_SCREEN`)                                                                                                                                             | accepted: 60.04's rule; a new registry entry changes no reference                                                                                                                                                                                                                                                                                                                                                          |
| legal: contents                  | The section being read is marked in the contents list (brass edge, the text's colour), the first at the top as the mockup draws it, and the mark follows the reader                                                           | fixed: CSS alone (`scroll-target-group: auto` on the list, `:target-current` on the link, in `src/part-b.css`), no script. The mark is a colour and the border `.toc a` already reserves, so nothing moves (CLS 0). A browser without `scroll-target-group` draws no mark, as the accepted Phase 1 row had it. There is no `aria-current="true"` in the markup: it would be wrong once the reader scrolls (review-phase-1) |
| 404                              | When the page is shorter than the window, a strip of the page's colour showed between the head's band and the footer (part B's display title is a few pixels shorter than part A's)                                           | fixed: on the 404, `<main>` is a column and its head grows to the footer (`src/part-b.css`)                                                                                                                                                                                                                                                                                                                                |
| 404                              | The fourth place was Troubleshooting, not Help; no `noindex`; the address line is written by the Worker                                                                                                                       | the fourth place: fixed by 60.12 (#1225), Help (`/help/`) with the mockup's words; the rest accepted, as in review-phase-1                                                                                                                                                                                                                                                                                                 |
| install chooser                  | Its part A layout (a compact head, then the guides by kind) with the display-face title                                                                                                                                       | accepted: no part B mockup; the guides themselves move with 60.05's own part B layout (`install-p3`)                                                                                                                                                                                                                                                                                                                       |
| social cards                     | Every moved page has part B's card (`cardSvgB`): the both-script wordmark, the page's title in the display face, and a foot line per kind: the chooser, the problem pages, the legal pages (`cardSub` in `scripts/cards.mjs`) | fixed                                                                                                                                                                                                                                                                                                                                                                                                                      |
| every page: footer, 360 px       | The copyright line and the language link stack in two rows, where the mockups set them on one                                                                                                                                 | accepted: the row wraps (`flex-wrap` in `shell.css`) because CI's Linux render sets the Latin a little wider; the part A references on main wrap the same way, so this move does not cause it                                                                                                                                                                                                                              |
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

## The help pages (60.12)

The help pages are compared at 360, 768 and 1280 px, in English and Arabic, dark, and light at
1280 px for the index: the built page's visual reference (`e2e/__screenshots__/`, rendered on Linux in the
CI image) beside its part B mockup (`mockups/png/`). The mockup's topic is "Search and YouTube";
the references are the help family's representatives (`e2e/representatives.json`): the longest
topic (`failures`), the shortest (`word-review`) and one with a table (`big-screen`). A topic's
reference is the page's first 4,000 pixels (`TALL` in `e2e/visual.spec.ts`), so it stays under
1 MB: the longest topic is 32,000 pixels tall at 360 px. The shortest shows its related topics at
every width, and its footer where it fits (768 and 1280 px). Every topic is checked by the structural tests (axe, keyboard, reflow, RTL,
layout shift) in both languages.

### Differences: the help index

Each difference between the built page and its mockup, fixed in the pull request or accepted.

| Where       | Difference                                                                                                                                                                    | Outcome                                                                                                                                                                                                                                                                                                                                                                                     |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| topics      | The index lists all 44 topics, as the app groups them; the mockup leaves out the four native-app topics (iPhone, iPad and Mac apps; Apple TV; Android; Android TV), citing Q5 | accepted, for the lead to confirm: story 60.12 publishes every topic of the app ("the same topics, not a second copy"), and its acceptance is 44 topic pages in each language; Q5 decides which apps the download page offers, not which help a household that already has an app may read. Leaving them out is `topics.ts` minus four ids in `scripts/help.mjs`, should the lead decide so |
| count       | The count reads "44 topics" («44 موضوعًا»); the mockup's reads "0 topics"                                                                                                     | fixed: the mockup never counted its list; the count follows the filter in the page's plural forms (`src/problems-filter.ts`)                                                                                                                                                                                                                                                                |
| same help   | The Arabic line reads «هذه هي المساعدة نفسها التي تجدونها في التطبيق، في قسم المساعدة.»; the mockup's ends «تحت «المساعدة»»                                                   | fixed: the story's strings table, in the site's voice                                                                                                                                                                                                                                                                                                                                       |
| filter      | The filter matches each topic's title, summary and headings (a `data-search` on each item); the mockup's matched what the card shows                                          | fixed: story 60.12 ("over titles and headings"); the script is the problem index's, which now reads any list marked `data-filter-list`                                                                                                                                                                                                                                                      |
| shell       | Download in the nav and footer goes to `/download/`, which is built after the help pages                                                                                      | fixed: a family's `paths` count as built for the families before it (`withDeclared` in `scripts/build.mjs`), so the help pages do not send Download to GitHub                                                                                                                                                                                                                               |
| social card | The card is part B's template, the index's title "How to use Maktaba at home", with the foot line "Help, the same as in the app"                                              | fixed: `isPartBCard` in `scripts/cards.mjs` covers `/help/…`                                                                                                                                                                                                                                                                                                                                |

### Differences: a help topic

| Where          | Difference                                                                                                                                                                            | Outcome                                                                                                                                                                                  |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| article        | The whole topic, every section; the mockup drew two sections                                                                                                                          | fixed: the mockup drew a slice to show the layout                                                                                                                                        |
| contents       | The section being read is marked in the contents list, the first at the top, as the mockup draws it                                                                                   | fixed: the legal pages' rule applies (#1223's `scroll-target-group` and `:target-current` in `src/part-b.css`, CSS alone); a browser without it draws no mark                            |
| In Maktaba     | A path longer than a phone's line wraps, its tint carried onto each line; the mockup keeps it on one line (`white-space: nowrap`)                                                     | fixed: on one line, "Administration → System health" widened the page past 320 px (1.4.10); `src/help.css`                                                                               |
| In Maktaba     | The arrow points the page's way (→ in English, ← in Arabic) whatever the source wrote; the label is the link's own words, as the mockup draws it                                      | fixed: the story ("the arrow is a direction mark and mirrors")                                                                                                                           |
| related topics | Three cards: the topics this one links to, in the order it first does, then its category's; the mockup chose three by hand, and its cards were indented by the article's list padding | fixed: the rule is `relatedOf` in `scripts/help.mjs`; the cards start at the article's edge (`src/help.css`)                                                                             |
| tables         | A topic's pipe table is a table: a muted header row, a rule under each row, cells that wrap on a phone                                                                                | added: no mockup draws one (the big screen's and the desktop player's keys, the storage topic's terminal); the table never scrolls sideways, which would need a keyboard stop of its own |
| code           | A topic's code is the shell's code block (its bar, "Commands", and Copy), its lines wrapping instead of scrolling                                                                     | added: the storage topic's commands; a scrolling block failed axe's scrollable-region-focusable                                                                                          |
| wide layout    | Below 1024 px the topic's column is `minmax(0, 1fr)`                                                                                                                                  | fixed: without it a wide table or command widened the grid past the viewport                                                                                                             |
| links          | A help link goes to the site's topic in the page's language; an external link stays as written                                                                                        | as the mockup                                                                                                                                                                            |
| topic text     | The topics are Epic 41's, published as written: some Arabic topics address the reader in the singular, where the site's own words use the plural                                      | accepted: the story publishes the app's text and does not fork it; a change to a topic's voice belongs in `web/src/help/content/ar/` and reaches the app and the site together           |

### The images: help

| Page                | Width      | Language | Built (reference)                                                                     | Mockup                                                                   |
| ------------------- | ---------- | -------- | ------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| index               | 360        | en       | [`help-360-en.png`](../e2e/__screenshots__/help-360-en.png)                           | [`help.en.360.dark.png`](mockups/png/help.en.360.dark.png)               |
| index               | 360        | ar       | [`help-360-ar.png`](../e2e/__screenshots__/help-360-ar.png)                           | [`help.ar.360.dark.png`](mockups/png/help.ar.360.dark.png)               |
| index               | 768        | en       | [`help-768-en.png`](../e2e/__screenshots__/help-768-en.png)                           | [`help.en.768.dark.png`](mockups/png/help.en.768.dark.png)               |
| index               | 768        | ar       | [`help-768-ar.png`](../e2e/__screenshots__/help-768-ar.png)                           | [`help.ar.768.dark.png`](mockups/png/help.ar.768.dark.png)               |
| index               | 1280       | en       | [`help-1280-en.png`](../e2e/__screenshots__/help-1280-en.png)                         | [`help.en.1280.dark.png`](mockups/png/help.en.1280.dark.png)             |
| index               | 1280       | ar       | [`help-1280-ar.png`](../e2e/__screenshots__/help-1280-ar.png)                         | [`help.ar.1280.dark.png`](mockups/png/help.ar.1280.dark.png)             |
| index               | 1280 light | en       | [`help-1280-en-light.png`](../e2e/__screenshots__/help-1280-en-light.png)             | none (dark only)                                                         |
| index               | 1280 light | ar       | [`help-1280-ar-light.png`](../e2e/__screenshots__/help-1280-ar-light.png)             | none (dark only)                                                         |
| topic: the shortest | 360        | en       | [`help-word-review-360-en.png`](../e2e/__screenshots__/help-word-review-360-en.png)   | [`help-topic.en.360.dark.png`](mockups/png/help-topic.en.360.dark.png)   |
| topic: the shortest | 360        | ar       | [`help-word-review-360-ar.png`](../e2e/__screenshots__/help-word-review-360-ar.png)   | [`help-topic.ar.360.dark.png`](mockups/png/help-topic.ar.360.dark.png)   |
| topic: the shortest | 768        | en       | [`help-word-review-768-en.png`](../e2e/__screenshots__/help-word-review-768-en.png)   | [`help-topic.en.768.dark.png`](mockups/png/help-topic.en.768.dark.png)   |
| topic: the shortest | 768        | ar       | [`help-word-review-768-ar.png`](../e2e/__screenshots__/help-word-review-768-ar.png)   | [`help-topic.ar.768.dark.png`](mockups/png/help-topic.ar.768.dark.png)   |
| topic: the shortest | 1280       | en       | [`help-word-review-1280-en.png`](../e2e/__screenshots__/help-word-review-1280-en.png) | [`help-topic.en.1280.dark.png`](mockups/png/help-topic.en.1280.dark.png) |
| topic: the shortest | 1280       | ar       | [`help-word-review-1280-ar.png`](../e2e/__screenshots__/help-word-review-1280-ar.png) | [`help-topic.ar.1280.dark.png`](mockups/png/help-topic.ar.1280.dark.png) |
| topic: a table      | 360        | en       | [`help-big-screen-360-en.png`](../e2e/__screenshots__/help-big-screen-360-en.png)     | [`help-topic.en.360.dark.png`](mockups/png/help-topic.en.360.dark.png)   |
| topic: a table      | 360        | ar       | [`help-big-screen-360-ar.png`](../e2e/__screenshots__/help-big-screen-360-ar.png)     | [`help-topic.ar.360.dark.png`](mockups/png/help-topic.ar.360.dark.png)   |
| topic: a table      | 1280       | en       | [`help-big-screen-1280-en.png`](../e2e/__screenshots__/help-big-screen-1280-en.png)   | [`help-topic.en.1280.dark.png`](mockups/png/help-topic.en.1280.dark.png) |
| topic: a table      | 1280       | ar       | [`help-big-screen-1280-ar.png`](../e2e/__screenshots__/help-big-screen-1280-ar.png)   | [`help-topic.ar.1280.dark.png`](mockups/png/help-topic.ar.1280.dark.png) |
| topic: the longest  | 360        | en       | [`help-failures-360-en.png`](../e2e/__screenshots__/help-failures-360-en.png)         | [`help-topic.en.360.dark.png`](mockups/png/help-topic.en.360.dark.png)   |
| topic: the longest  | 1280       | ar       | [`help-failures-1280-ar.png`](../e2e/__screenshots__/help-failures-1280-ar.png)       | [`help-topic.ar.1280.dark.png`](mockups/png/help-topic.ar.1280.dark.png) |

The references at 768 px of `big-screen` and `failures`, and the rest of their languages, are in
`e2e/__screenshots__/` beside these.

## Sign-off

| Date | Reviewer | Commit | Outcome                                                     | Changes asked |
| ---- | -------- | ------ | ----------------------------------------------------------- | ------------- |
|      | the lead |        | to sign: the moved pages (#1223) and the help pages (#1225) |               |

**Open before Phase 3 is done:**

- the lead's call on the four native-app topics on the index (first row above);
- the owner's own read of the Arabic help copy the site adds (the strings table of story 60.12;
  Q2, A5): the topics' own text is Epic 41's;
- pricing (60.13), once Epic 50 decides the plans.

## Not claimed here

The owner's read of the Arabic copy (Q2, A5) is still open; nothing in this review stands for it.
