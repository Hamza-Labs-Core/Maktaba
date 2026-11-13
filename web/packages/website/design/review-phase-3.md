# Phase 3 review: the built pages beside part B's mockups

**Status: the help pages (story 60.12) are written up below for the lead's review and signature.
The install, problem and legal pages' move onto part B (60.05, 60.06, 60.07) and pricing (60.13)
add their own sections when they ship.**

Each page is compared at 360, 768 and 1280 px, in English and Arabic, dark, and light at 1280 px
for the index: the built page's visual reference (`e2e/__screenshots__/`, rendered on Linux in the
CI image) beside its part B mockup (`mockups/png/`). The mockup's topic is "Search and YouTube";
the references are the help family's representatives (`e2e/representatives.json`): the longest
topic (`failures`), the shortest (`word-review`) and one with a table (`big-screen`). A topic's
reference is the page's first 4,000 pixels (`TALL` in `e2e/visual.spec.ts`), so it stays under
1 MB: the longest topic is 32,000 pixels tall at 360 px. The shortest shows its related topics at
every width, and its footer where it fits (768 and 1280 px). Every topic is checked by the structural tests (axe, keyboard, reflow, RTL,
layout shift) in both languages.

## Differences: the help index

Each difference between the built page and its mockup, fixed in the pull request or accepted.

| Where       | Difference                                                                                                                                                                    | Outcome                                                                                                                                                                                                                                                                                                                                                                                     |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| topics      | The index lists all 44 topics, as the app groups them; the mockup leaves out the four native-app topics (iPhone, iPad and Mac apps; Apple TV; Android; Android TV), citing Q5 | accepted, for the lead to confirm: story 60.12 publishes every topic of the app ("the same topics, not a second copy"), and its acceptance is 44 topic pages in each language; Q5 decides which apps the download page offers, not which help a household that already has an app may read. Leaving them out is `topics.ts` minus four ids in `scripts/help.mjs`, should the lead decide so |
| count       | The count reads "44 topics" («44 موضوعًا»); the mockup's reads "0 topics"                                                                                                     | fixed: the mockup never counted its list; the count follows the filter in the page's plural forms (`src/problems-filter.ts`)                                                                                                                                                                                                                                                                |
| same help   | The Arabic line reads «هذه هي المساعدة نفسها التي تجدونها في التطبيق، في قسم المساعدة.»; the mockup's ends «تحت «المساعدة»»                                                   | fixed: the story's strings table, in the site's voice                                                                                                                                                                                                                                                                                                                                       |
| filter      | The filter matches each topic's title, summary and headings (a `data-search` on each item); the mockup's matched what the card shows                                          | fixed: story 60.12 ("over titles and headings"); the script is the problem index's, which now reads any list marked `data-filter-list`                                                                                                                                                                                                                                                      |
| shell       | Download in the nav and footer goes to `/download/`, which is built after the help pages                                                                                      | fixed: a family's `paths` count as built for the families before it (`withDeclared` in `scripts/build.mjs`), so the help pages do not send Download to GitHub                                                                                                                                                                                                                               |
| social card | The card is part B's template, the index's title "How to use Maktaba at home", with the foot line "Help, the same as in the app"                                              | fixed: `isPartBCard` in `scripts/cards.mjs` covers `/help/…`                                                                                                                                                                                                                                                                                                                                |

## Differences: a help topic

| Where          | Difference                                                                                                                                                                            | Outcome                                                                                                                                                                                  |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| article        | The whole topic, every section; the mockup drew two sections                                                                                                                          | fixed: the mockup drew a slice to show the layout                                                                                                                                        |
| contents       | The list does not mark its first entry as current (the mockup draws `aria-current` on it)                                                                                             | accepted: as the legal pages in Phase 1, the mark would be wrong once the reader scrolls, and no script follows the scroll                                                               |
| In Maktaba     | A path longer than a phone's line wraps, its tint carried onto each line; the mockup keeps it on one line (`white-space: nowrap`)                                                     | fixed: on one line, "Administration → System health" widened the page past 320 px (1.4.10); `src/help.css`                                                                               |
| In Maktaba     | The arrow points the page's way (→ in English, ← in Arabic) whatever the source wrote; the label is the link's own words, as the mockup draws it                                      | fixed: the story ("the arrow is a direction mark and mirrors")                                                                                                                           |
| related topics | Three cards: the topics this one links to, in the order it first does, then its category's; the mockup chose three by hand, and its cards were indented by the article's list padding | fixed: the rule is `relatedOf` in `scripts/help.mjs`; the cards start at the article's edge (`src/help.css`)                                                                             |
| tables         | A topic's pipe table is a table: a muted header row, a rule under each row, cells that wrap on a phone                                                                                | added: no mockup draws one (the big screen's and the desktop player's keys, the storage topic's terminal); the table never scrolls sideways, which would need a keyboard stop of its own |
| code           | A topic's code is the shell's code block (its bar, "Commands", and Copy), its lines wrapping instead of scrolling                                                                     | added: the storage topic's commands; a scrolling block failed axe's scrollable-region-focusable                                                                                          |
| wide layout    | Below 1024 px the topic's column is `minmax(0, 1fr)`                                                                                                                                  | fixed: without it a wide table or command widened the grid past the viewport                                                                                                             |
| links          | A help link goes to the site's topic in the page's language; an external link stays as written                                                                                        | as the mockup                                                                                                                                                                            |
| topic text     | The topics are Epic 41's, published as written: some Arabic topics address the reader in the singular, where the site's own words use the plural                                      | accepted: the story publishes the app's text and does not fork it; a change to a topic's voice belongs in `web/src/help/content/ar/` and reaches the app and the site together           |

## The images: help

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

| Date | Reviewer | Commit | Outcome                         | Changes asked |
| ---- | -------- | ------ | ------------------------------- | ------------- |
|      | the lead |        | to sign: the help pages (60.12) |               |

**Open before Phase 3 is done:**

- the lead's call on the four native-app topics on the index (first row above);
- the owner's own read of the Arabic help copy the site adds (the strings table of story 60.12;
  Q2, A5): the topics' own text is Epic 41's;
- the install, problem and legal pages on part B (60.05, 60.06, 60.07), and pricing (60.13).
