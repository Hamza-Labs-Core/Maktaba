# maktaba.tv design: the shell (part A) and the direction (part B)

The written spec of the site (story 60.01). Part A is the shell that Phase 1's pages are built
on: its tokens, type, components and layouts. Part B, from
[Part B: the direction](#part-b-the-direction) on, adds the brand look, the wordmark, the
display face, the colour extension, imagery and icons, and the layouts of home, download,
install, help and the Phase 3 pages. Every layout has mockups at 360, 768 and 1280 px in
English and Arabic.

## What is here

| Path                                      | What it is                                                                                                               |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `site-tokens.css`                         | the site's tokens, **generated** from `web/design-system/tokens/` by `build-tokens.mjs` (do not edit)                    |
| `fonts.css`                               | the `@font-face` rules: the text face, the display face and their metric-matched fallbacks                               |
| `site.css`                                | part B's page components (hero, frames, download cards, help), on top of `shell.css`                                     |
| `shell.css`                               | the shell: every component and layout below, logical properties only                                                     |
| `shell.js`                                | the phone menu, copy buttons, the index filter and the language hint (about 3 KB, no framework)                          |
| `mockups/make.mjs`                        | writes `mockups/*.html` from the shell and each page's copy (`node mockups/make.mjs`)                                    |
| `mockups/shell.mjs`, `mockups/part-b.mjs` | the shell's HTML (header, footer, wordmark, page); part B's pages, read from `../copy/*.md`                              |
| `mockups/*.html`                          | part A: front door, legal, problem, problem index, install guide, 404, component sheet; part B: below                    |
| `mockups/png/`                            | their renders (`node render.mjs`): 134 PNGs, each under 1 MB, reduced to a 256-colour palette by `quantize.py` (18.1 MB) |
| `render.mjs`, `png.mjs`, `quantize.py`    | the render (Playwright, a local server, the 320 px reflow check), the PNG chunk reader, the palette step                 |
| `review-sheet.mjs`                        | writes `review-sheet.html` from the same list of renders                                                                 |
| `mockups/vendor/install.css`              | a copy of Epic 59's guide stylesheet at `a4c35d7eb`, for the install mockup                                              |
| `mockups/src/unraid.*.html`               | the built Unraid guide's header and main, the screenshot placeholders left out (60.05)                                   |
| `review-sheet.html`                       | every PNG on one page, by mockup, width and language, for review                                                         |
| `review-phase-1.md`                       | the built pages beside these mockups, each difference fixed or accepted (Phase 1 review, a draft until signed)           |
| `tests/`                                  | T01, T02's site half, T03, T04 and the copy checks; `make test-tokens` runs them                                         |
| `fonts/`                                  | the subset and rename scripts behind `../static/fonts/`                                                                  |

`make test-tokens` (the `unit_tokens` CI job) builds the tokens with `--check` (the site's CSS
must be current), then runs `tests/`: the mockups current with `make.mjs`, every PNG present,
under 1 MB and without metadata, every colour pair below at its ratio, no physical CSS
property, the fonts inside the budget, no refused word in the English copy, and each Arabic
page under 20% Latin.

## Direction in one paragraph

A household's own library at night: ink and brass, the app's own palette (Epic 48's Ink &
Brass), calm and unhurried. The pages are mostly words set well: one face, two weights, wide
margins, a measure of about 70 characters. The one decoration is the arch from the app icon,
drawn as two brass lines with the lamp's light inside it over an open book, at the end of the
line on the front door and the 404 at desktop width. Everything else is quiet: hairline rules
instead of boxes, brass only where something means something (a link, the current page, a
step, the call to action). Dark is the default; light follows the system.

## Tokens

The `site` group sits in `web/design-system/tokens/tokens.json` beside Epic 17's groups, with its
dark values in `tokens.dark.json`. `build-tokens.mjs` writes `site-tokens.css` from it: dark on
`:root`, and the base (light) value of every token the dark theme changes under
`prefers-color-scheme: light`. It emits the colours, spacing, radii, elevation, motion and
`site`; the app's hand-written `web/src/styles/tokens.css` is untouched, and `make test-tokens`
fails if it gains a `--site-` token.

| Key                                                                                                 | Dark                                  | Light           | For                                                       |
| --------------------------------------------------------------------------------------------------- | ------------------------------------- | --------------- | --------------------------------------------------------- |
| `site.type.family`                                                                                  | Maktaba Text, its fallback, system-ui |                 | both scripts                                              |
| `site.type.mono`                                                                                    | the system monospace                  |                 | code (no download)                                        |
| `site.type.display-1…3`                                                                             | 56/64, 44/52, 34/42                   |                 | part B's headlines (Latin)                                |
| `site.type-arabic.display-1…3`                                                                      | 60/84, 48/68, 37/54                   |                 | part B's headlines (Arabic)                               |
| `site.type.heading-1`, `heading-1-phone`, `heading-2`, `heading-3`, `lead`, `body`, `small`, `code` | see the scale                         |                 | the shell's text                                          |
| `site.type-arabic.*`                                                                                | see the scale                         |                 | the same steps in Arabic                                  |
| `site.measure.text`                                                                                 | 36rem                                 |                 | about 70 Latin characters at body size, about 60 Arabic   |
| `site.measure.wide`                                                                                 | 72rem                                 |                 | the frame of header, footer and grids                     |
| `site.surface.hero`                                                                                 | `#0E1624`                             | `#FBF5E8`       | the page head band                                        |
| `site.surface.section`                                                                              | `#0F1725`                             | `#FFF9EA`       | footer, callouts, side boxes                              |
| `site.surface.shot`                                                                                 | `#131D2E`                             | `#F4EAD3`       | the screenshot backdrop (part B)                          |
| `site.surface.code`                                                                                 | `#131D2E`                             | `#FBF5E8`       | code and compose blocks, inline code                      |
| `site.line.rule`                                                                                    | `#22304A`                             | `#EFE3C8`       | hairlines that only separate                              |
| `site.line.control`                                                                                 | `#6B7A94`                             | `#8C7A55`       | a control's edge (3:1)                                    |
| `site.accent.cta`                                                                                   | brand 500                             | brand 500       | the call to action's fill                                 |
| `site.accent.cta-hover`                                                                             | brand 300                             | brand 600       | the same, hovered                                         |
| `site.accent.cta-fg`                                                                                | `#1A1408`                             | `#1A1408`       | text on the call to action                                |
| `site.accent.focus`                                                                                 | brand 400                             | brand 700       | the focus ring                                            |
| `site.accent.mark`                                                                                  | brand 500                             | brand 700       | brass as a mark: current page, step ring, edges, the arch |
| `site.callout.note`, `warning`, `tip`                                                               | info, warn, success                   | the same, light | callout edges and icons                                   |
| `site.space.section`                                                                                | clamp(48px, 8vw, 96px)                |                 | between page bands                                        |
| `site.space.gutter`                                                                                 | clamp(16px, 4vw, 32px)                |                 | the page's inline padding                                 |

Part A fixed the names; part B may move the values.

### Contrast (T03)

Every pair the shell paints, from `tests/contrast.test.mjs` (WCAG 2.2: text 4.5:1, large text
and UI parts 3:1).

| Foreground                 | Background              | Where                         | Needs | Dark                       | Light                      |
| -------------------------- | ----------------------- | ----------------------------- | ----- | -------------------------- | -------------------------- |
| fg                         | bg, hero, section, code | body text                     | 4.5   | 15.95, 15.29, 15.15, 14.26 | 17.68, 16.28, 16.83, 16.28 |
| muted                      | bg, hero, section, code | secondary text, footer, slugs | 4.5   | 8.94, 8.57, 8.49, 7.99     | 7.91, 7.28, 7.53, 7.28     |
| accent-text                | bg, hero, section, code | links, step numbers           | 4.5   | 9.68, 9.28, 9.20, 8.65     | 5.47, 5.04, 5.21, 5.04     |
| cta-fg                     | cta, cta-hover          | the call to action            | 4.5   | 9.37, 11.77                | 9.37, 6.79                 |
| fg                         | accent-soft over hero   | the "who sees this" pill      | 4.5   | 11.81                      | 14.58                      |
| callout.tip                | code                    | the Copied label              | 4.5   | 7.63                       | 5.83                       |
| line.control               | bg, hero, section, code | control edges                 | 3     | 4.35, 4.17, 4.14, 3.89     | 4.17, 3.84, 3.97, 3.84     |
| accent.focus               | bg, hero, section, code | focus ring                    | 3     | 10.81, 10.36, 10.27, 9.66  | 5.47, 5.04, 5.21, 5.04     |
| accent.mark                | bg, hero                | current page, step ring, arch | 3     | 9.68, 9.28                 | 5.47, 5.04                 |
| callout.note, warning, tip | section                 | callout edges and icons       | 3     | 6.24, 8.22, 8.11           | 5.70, 5.71, 6.03           |

`line.rule` is below 3:1 on purpose: a rule only separates, and nothing is understood from it
alone (WCAG 1.4.11 covers what is needed to understand or operate).

## Type

**One face, Maktaba Text** (IBM Plex Sans Arabic 1.1, the apps' face, subset and renamed as the
OFL asks: `../static/fonts/README.md`). Weights **400 and 600** only: the shell uses no other.
Each weight is split by `unicode-range` into a Latin and an Arabic file, so an English page
downloads 29 KB of fonts and an Arabic page 83 KB, inside the 150 KB budget with room for part
B's display face. Preloaded, `font-display: optional` (below).

**No layout shift.** The faces are `optional`: a preloaded face arrives in the first moments
and is drawn from the first paint; one that comes later is not swapped in, so a line never moves
(the built site measured a shift of 0.00005 with `swap` on Linux, where Arial is missing). Should
a face fall back, `Maktaba Text Fallback` is Arial scaled to Plex's metrics:
`size-adjust: 101.6%` (400) and `97.15%` (600) from the average Latin advance, with
`ascent-override` and `descent-override` from Plex's 1.085/0.415 em. Every text style also sets
an explicit line height, so a line box is the same height in either face. Code uses the
system monospace and downloads nothing.

**The scale, side by side** (size / line height in px). Arabic is set one step larger and
looser at every step: its letters sit smaller on the same body, and its marks and dots need
the lead.

| Step                      | Latin   | Arabic  | Weight | Used for                    |
| ------------------------- | ------- | ------- | ------ | --------------------------- |
| display-1                 | 56 / 64 | 60 / 84 | 600    | part B                      |
| display-2                 | 44 / 52 | 48 / 68 | 600    | part B                      |
| display-3                 | 34 / 42 | 37 / 54 | 600    | part B                      |
| heading-1 (768 px and up) | 36 / 44 | 38 / 56 | 600    | a page's title              |
| heading-1 (phone)         | 30 / 38 | 32 / 48 | 600    | a page's title under 768 px |
| heading-2                 | 24 / 32 | 26 / 40 | 600    | sections                    |
| heading-3                 | 19 / 28 | 20 / 32 | 600    | steps, groups, side boxes   |
| lead                      | 19 / 30 | 20 / 34 | 400    | the paragraph under a title |
| body                      | 17 / 28 | 18 / 32 | 400    | text                        |
| small                     | 14 / 22 | 15 / 26 | 400    | meta, footer, details       |
| code                      | 14 / 22 | 14 / 22 | 400    | code (always Latin)         |

- Titles balance their lines (`text-wrap: balance`) and stop at 22 characters a line in
  Latin, 20 in Arabic.
- Latin titles tighten by 0.01 em; Arabic is never letter-spaced (it breaks the joins).
- No all-caps labels, no italics for emphasis in Arabic (the script has none): bold, sparingly.

## Components

Every size is from the tokens. Every target is at least 44 × 44 px (WCAG 2.5.8 asks 24), except
links inside running text.

### Header and nav

- **Anatomy:** the mark (the app icon at 32 px and the name, «مكتبة» on Arabic pages) at the
  start; the nav (Install, Messages, Download) and the language switch at the end; a hairline
  under the bar. 64 px high.
- **States:** a link's hover fills it with `surface.section`; the **current page** has a 2 px
  brass rule under the word and `aria-current="page"`; focus is the shell's ring.
- **Phone (under 768 px):** the nav folds behind a **Menu** button (`aria-expanded`,
  `aria-controls`); the language switch stays in the bar. The open menu drops under the bar
  across the page, links at lead size, 52 px rows, the current one marked by a 3 px brass edge
  at the start. Escape closes it and returns focus to the button. Under 400 px the switch and
  the button drop their icons so all three fit at 320 px.
- **Without script:** the links stay a wrapped row under the mark; nothing is hidden.
- **RTL:** the bar mirrors through logical properties; the external-link icon mirrors.
- **Accessible names:** `<nav aria-label="Main">` / «الرئيسية»; the mark's image has `alt=""`
  (the name is beside it).

### Language switch

- A **link to the twin page**, never a menu: `href` to the same page in the other language,
  labelled in that language (**العربية** on an English page, **English** on an Arabic one),
  with `hreflang` and `lang` on the link (and `dir="ltr"` on «English» in an Arabic page), so a
  screen reader says it in its own language. A globe icon before it, `aria-hidden`.
- A pill with a control edge; hover turns the edge brass. Repeated in the footer as a sentence
  («اقرأ هذه الصفحة بالعربية» / "Read this page in English").
- **The language hint** (§6.5): on an English page in a browser that prefers Arabic, a thin band
  above the page offers the Arabic page, written in Arabic, with **Hide**; hiding it sets
  `localStorage` (`maktaba.langHint`), never a cookie. And the other way round.

### Footer

- **Anatomy:** the mark and two sentences on what Maktaba is; four groups of links: **Get
  Maktaba** (install guides, downloads on GitHub), **Help** (help topics, messages), **Legal**
  (privacy, terms, copyright), **Elsewhere** (maktaba.dev, source on GitHub); then a base row with
  the copyright line and the language link.
- **Widths:** two columns of groups under 1024 px (the about block across both), five columns
  from 1024 px.
- External links carry the external icon (mirrors in RTL), 16 px, kept beside the last word.
- `<nav aria-label="Site">` / «روابط الموقع»; group titles are `h2`s at small size.

### Buttons

- **Primary:** brass fill, ink text, 600 weight, 48 px high; hover `cta-hover`. One per screen.
- **Quiet:** no fill, a control edge, fg text; hover turns the edge brass.
- An arrow after a label means "onwards" and mirrors in RTL; an external icon means "leaves the
  site". Labels **wrap**, never truncate: the component sheet shows the longest real Arabic
  label at 360 px on two lines.

### Callouts (note, warning, tip)

- **Anatomy:** a section-surface box, a 3 px edge at the start in the kind's colour, the kind's
  icon (info circle, triangle, lamp), then a bold label word (**Note:**, **Warning:**, **Tip:** /
  «ملاحظة:» «تحذير:» «نصيحة:») and the text. Colour is never the only signal: the word says the
  kind.
- `role="note"`; the icon is `aria-hidden`.

### Code and compose blocks

- **Anatomy:** a bar with what the block is ("In the container's Console", "The compose file:
  `docker-compose.yml`") and a **Copy** button at the end; then the code, monospace 14/22,
  scrolling sideways on its own when long (the page never does).
- **Always left to right,** also inside an Arabic page: `<pre>` has `direction: ltr` and
  `unicode-bidi: isolate`; the bar follows the page's direction, so on an Arabic page the label
  sits at the right and the button at the left.
- **Copy:** visible label **Copy** / «نسخ»; accessible name "Copy the command" / «نسخ الأمر»
  (it contains the visible label, WCAG 2.5.3). After a press the label reads **Copied** / «تم
  النسخ» in the tip colour for 2 seconds, and the same word goes into the page's polite live
  region, so a screen reader hears it. Without a clipboard the button does nothing and the
  text stays selectable.
- **Inline code** in prose: a code-surface chip, isolated (`direction: ltr; unicode-bidi:
isolate`), wrapping at spaces.

### The step list

- An `<ol>`; each step a number in a 36 px brass ring at the start, a 2 px thread down to the
  next, a `heading-3` title, the text, and an optional code block or (part B) screenshot.
- Numbers are Western digits in both languages, tabular; the ring and thread sit at the start
  edge, so they move to the right in Arabic.

### Skip link and focus

- **Skip link:** the first stop, "Skip to the page" / «الانتقال إلى محتوى الصفحة», hidden above the
  viewport until focused, then a brass button at the top start.
- **Focus:** `:focus-visible` draws a 2 px `accent.focus` ring 3 px off the element, on every
  link, button and field. The component sheet draws it without a keyboard.

## Layouts

All layouts sit in the frame (`measure.wide`, gutter `clamp(16px, 4vw, 32px)`) and reflow to one
column at 320 px without sideways scrolling: `render.mjs` loads every mockup at 320 px and fails
on any overflow (WCAG 1.4.10).

| Layout                         | Mockup          | Shape                                                                                                                                                                                                                                            |
| ------------------------------ | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Text page (front door)**     | `front-door`    | the page head with title, lead and two buttons, the arch at the end from 1024 px; three facts with brass edges (three columns from 768 px); four ways in, separated by rules (two columns from 768 px)                                           |
| **Text page (legal)**          | `legal`         | a compact head with title and last-changed date; an "In short" box; the sections; from 1024 px a contents list (15rem, sticky) at the start beside the article                                                                                   |
| **Problem page**               | `problem`       | back to Messages, the title, the "who sees this" pill; What this means, What you can do (steps), Who can help; side boxes for related pages and details for developers (status, type URI, code), beside the text from 1024 px and under it below |
| **Problem index**              | `problems`      | title, lead, the **filter** (`role="search"`, a label, a live count, filtering in the page with no request); the areas as titled lists, two columns from 768 px; each entry's title and slug (monospace, LTR)                                    |
| **Install guide in the shell** | `install-guide` | the shell's header and footer around Epic 59's guide, which keeps its own stylesheet                                                                                                                                                             |
| **404**                        | `404`           | the page head with "This page isn't here", four places to go as tiles (two columns from 768 px), and the address asked for in code; the arch from 1024 px                                                                                        |

**The install guide in the shell.** The guide's stylesheet sets a width and padding on `<body>`,
so the shell loads after it, gives the body back (`body.in-shell`) and puts the guide in the
frame (`.guide-frame`), with the shell's face and sizes for its title and lead. The guide's own
nav line (all NAS guides, the other language) stays. Its screenshot placeholders are left out,
as 60.05 says. One clash for 60.05 to settle in the install package: the guide's
`[dir="rtl"] button` and `.guide pre { text-align: left }` are physical, so they stay in
`vendor/install.css` and outside T04.

## Bidi: Arabic and Latin in one line

- **Code, commands, paths, URIs:** `<code>`, isolated and LTR (above). Never a bare path in
  Arabic prose.
- **A Latin name in Arabic prose** (a product, Docker, GitHub): plain text is enough when it
  stands between Arabic words. When it ends a sentence or runs into punctuation, or a run holds
  more than one Latin word with symbols (`Admin → Updates`), wrap it in `<bdi>` (with
  `lang="en"`), so the punctuation lands on the Arabic side.
- **A Latin menu name inside « »:** the guillemets stay Arabic punctuation and the name sits
  inside them: «Container Path».
- **A number after Latin text** («Unraid ‏6.12»): Epic 59 puts a right-to-left mark (U+200F) before
  the digits so they stay with the Arabic that follows; the shell keeps that rule.
- **The copyright line** («© 2026 Hamza Labs») is a `dir="ltr"` span on Arabic pages.
- **An Arabic run on an English page** (the language hint): its own `lang="ar" dir="rtl"`.

## RTL and icons

- One stylesheet for both directions: logical properties only (`margin-inline-start`,
  `inset-inline-end`, `border-inline-start`), refused otherwise by T04.
- **Mirror** (class `dir-icon`): the onward arrow, the back chevron, the external-link arrow.
- **Never mirror:** the app icon, the arch (it is symmetric), the globe, menu, copy, search,
  callout icons, the person icon, code, and (part B) screenshots, play and media controls,
  platform marks, checkmarks.
- Icons: 24-unit grid, drawn at 20 px, 1.75 stroke, round caps and joins, `currentColor`,
  `aria-hidden="true"`.

## Forced colours, motion, print

- `forced-colors: active`: focus rings use `Highlight`; buttons, fields, copy buttons, callouts,
  code blocks, boxes and the step rings use `CanvasText` borders; the current page is
  underlined; the arch is hidden.
- `prefers-reduced-motion`: the only motion is a 120 ms colour change on hover, and it is off
  under reduce.
- Print hides the nav, menu, copy buttons and the language hint.

## Decided at part A's review (2026-11-11)

- **The font's name: "Maktaba Text".** The OFL reserves "Plex", so the subsets keep the
  renamed family. Serving IBM's unmodified complete files would keep the name at about twice the
  bytes (72 KB a weight) for no gain a visitor sees.
- **Address in Arabic: the household plural** («بيتكم»، «يمكنكم»), buttons named with the
  verbal noun («نسخ»). 60.06's strings follow it (#1180); the registry's own Arabic texts move to
  it next.
- **The name in Arabic: «مكتبة»** (Q3). Epic 59's Arabic guides, which write the name in Latin,
  move to it.

## Review

The lead reviews every image at every width in both languages (`review-sheet.html`) and records
the outcome here: approved at a commit, or the changes asked for. No Phase 1 page story opens
its pull request before part A is approved.

| Part                  | Date       | Reviewer                           | Commit                                           | Outcome                                                                                      | Changes asked                                                                       |
| --------------------- | ---------- | ---------------------------------- | ------------------------------------------------ | -------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| A, the shell          | 2026-11-11 | the owner, after the lead's review | fb0cb05a3 (renders); 8c88225b7 (formatting only) | approved: the look, the voice guide and the wordmark                                         | none; the font name stays "Maktaba Text" (below)                                    |
| B, the full direction | 2026-11-12 | the owner, after the lead's review | 59efc591c (renders)                              | approved: the direction, the wordmark (Q3), the display face Amiri as "Maktaba Display" (Q9) | the tagline: proposal 1 (Q1); the Arabic copy still waits for the owner's read (Q2) |

# Part B: the direction

Stories 60.01 part B and 60.02 part B. Part A's decisions stand: ink and brass, dark by
default and light by preference, the one text face, the arch, the household plural, «مكتبة»
and the wordmark. Part B adds to them; it changes none.

## Brand direction

A household's own library at night, with the lights low and one lamp on. The site sells a
quiet thing (your films, kept at home, on every screen) and it looks quiet: ink fields, brass
only where it means something, headlines in a book face, and the app's own screens shown in
plain frames, never staged in a room or a hand. Every page shows the real app on test data,
not illustrations. Three boards, described in words:

1. **The reading room at night.** Deep ink blue going to black at the edges; a single warm
   light falling on a page; brass fittings, not gold leaf. Hairline rules, wide margins, a
   serif headline that looks set, not typed. (The hero's glow behind the screenshot, the
   section bands, the arch at the end of a line.)
2. **The good book jacket.** Two faces only, one of them with a calligraphic Arabic: a large
   headline in Amiri, short and true, and a plain line under it. The name in both scripts,
   side by side, a brass rule between them. (The wordmark lockups, the social card, the
   display scale.)
3. **The screen as the photograph.** The product shot is the one picture: an app screen in a
   symmetric, near-black frame, sitting on a slightly lighter band, with its name and one line
   under it. Nothing else competes: no people, no stock photography, no device brand marks.
   (The device frames, the screenshot band, the "on every screen" shelf.)

## Wordmark (Q3)

The app icon beside the name in the display face (`wordmark()` in `mockups/shell.mjs`; sheet:
`mockups/wordmark.*.html`).

| Lockup        | Where                    | Icon  | Name                                                    | Gap   |
| ------------- | ------------------------ | ----- | ------------------------------------------------------- | ----- |
| Single script | the header               | 32 px | the page's language only: "Maktaba" or «مكتبة», 23 px   | 11 px |
| Both scripts  | the footer, social cards | 28–56 | the page's language first, a brass rule, then the other | 0.35× |

- **Direction:** on an Arabic page the lockup runs right to left (icon at the right, «مكتبة»,
  rule, "Maktaba"); on an English page the mirror. The icon itself never mirrors.
- **Minimum sizes:** the lockup down to a 24 px icon; under 24 px the icon stands alone
  (favicon 16 and 32, the home-screen icons).
- **Clear space:** half the icon's width on every side, with no text or edge inside it.
- **Surfaces:** the page (ink), the hero and the section band in dark, and the light page;
  the icon keeps its own dark tile on all of them.
- **Never:** recolour the icon or set it on a light tile; stretch, rotate or mirror it; set
  the name in another face, or letter-space the Arabic; put both names on one line without
  the rule.
- **The icon as SVG:** `../static/icon.svg` (3 KB) is traced from `web/public/icons/icon-512.png`
  (the arch, the open book and the lamp, symmetrised; the gradients sampled from the PNG). It
  matches the PNG at every size the site uses (16 to 72 px). At 512 px one difference shows:
  the tops of the book's pages next to the spine are slightly flatter under the lamp. The
  PNGs stay the source for the app; the site draws the SVG through one `<symbol>`.

## Display face (Q9): Amiri Bold, as "Maktaba Display"

Headlines (the hero, section heads, a page's `h1`, the social card) are set in Amiri Bold;
everything else stays in Maktaba Text. Amiri first, because:

- it is a Naskh drawn from the Bulaq press tradition: a book face, which is what a library
  should look like, and the warmth part A's ink and brass ask for;
- it has a real Latin companion in the same family, so "Maktaba" and «مكتبة» sit together
  in one face, at one weight, with no mismatch in the lockup;
- it is OFL 1.1, maintained, and complete in the shaping an Arabic headline needs
  (contextual forms, ligatures, mark positioning, all kept in the subset);
- its bold stays open and readable at the phone sizes (32–44 px), where a hairline or
  decorative display Arabic would not.

| File                               | Characters                                        | Size  |
| ---------------------------------- | ------------------------------------------------- | ----- |
| `maktaba-display-700-latin.woff2`  | Basic Latin, Latin-1, punctuation                 | 19 KB |
| `maktaba-display-700-arabic.woff2` | the Arabic letters, marks, digits and punctuation | 38 KB |

- **Budget:** an English page loads 49 KB of fonts, an Arabic page 142 KB, inside 150 KB per
  page per language (`tests/mockups.test.mjs` fails over it). Display files are preloaded on
  part B pages (English: Latin; Arabic: both).
- **Name:** the subset is a Modified Version under the OFL, so it is renamed "Maktaba
  Display" ("Amiri" is not reserved, but the rename keeps one rule for both faces);
  `../static/fonts/OFL-Amiri.txt` sits beside it.
- **Fallback:** "Maktaba Display Fallback", local Times New Roman Bold with `size-adjust:
99.5%`, `ascent-override: 113%`, `descent-override: 63.7%`, `line-gap-override: 0%`, so a
  headline does not move when Amiri arrives (`font-display: swap`).
- **Type scale** (tokens `site.type.display-*` and `site.type-arabic.display-*`; Arabic runs
  larger and much taller, for Amiri's deep descenders and marks):

| Step            | Used for                                             | Latin   | Arabic   |
| --------------- | ---------------------------------------------------- | ------- | -------- |
| display-1       | the home hero, ≥ 1024 px                             | 60 / 68 | 66 / 100 |
| display-1-phone | the home hero, < 768 px (768–1023: display-2's size) | 40 / 48 | 44 / 68  |
| display-2       | a page's `h1` (download, install, help)              | 44 / 52 | 48 / 76  |
| display-2-phone | the same, < 768 px                                   | 32 / 40 | 36 / 56  |
| display-3       | section heads on home                                | 32 / 40 | 36 / 56  |
| social card     | the tagline on the 1200 × 630 card                   | 68 / 80 | 72 / 108 |

## Colour extension

Part B's surfaces and their tested ratios (`tests/contrast.test.mjs`; text 4.5:1, large text
and UI parts 3:1). New tokens: `site.surface.frame`, `site.line.frame`, `site.accent.glow`;
the rest are part A's, used in new places.

| Role    | Token (dark / light)                                           | Pairs and ratios (dark / light)                                                                       |
| ------- | -------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| hero    | `site.surface.hero` #0E1624 / #FBF5E8, with `site.accent.glow` | headline 11.81 / 14.30; lead 6.62 / 6.39 (over the glow)                                              |
| section | `site.surface.section` #0F1725 / #FFF9EA                       | text 15.15 / 16.83; muted 8.49 / 7.53; links 9.20 / 5.21; badge 11.65 / 14.98; card edges 4.14 / 3.97 |
| shot    | `site.surface.shot` #131D2E / #F4EAD3                          | caption name 14.26 / 14.78; caption 7.99 / 6.61; kicker 8.65 / 4.58; shelf line 8.65 / 4.58           |
| cta     | `site.accent.cta` #E3B252 (both)                               | label 9.37 / 9.37                                                                                     |
| hover   | `site.accent.cta-hover` #EFCB7C / #C8952C                      | label 11.77 / 6.79                                                                                    |
| frame   | `site.surface.frame` #06090F / #1B1F27, edge `site.line.frame` | holds a screenshot only; no text on it, so no ratio applies                                           |
| glow    | `site.accent.glow` rgba(227,178,82,.14) / rgba(200,149,44,.16) | decorative; the text over it is tested as composited                                                  |

The frame stays near-black in light mode too: the app's screens are dark (the screenshots
are dark stand-ins), and a dark bezel reads as a device on a light page.

## Imagery and icons

- **Device frames** (`site.css`, `[data-kind]`), symmetric, so they never mirror:

| Kind    | Frame                                              | Shows                               |
| ------- | -------------------------------------------------- | ----------------------------------- |
| tv      | 1.1% bezel, 10 px radius, a stand 34% of the width | big screen                          |
| phone   | 3.4% bezel, rounded, no notch, no buttons          | the web app on a phone              |
| tablet  | 2.6% bezel                                         | the web app on a tablet             |
| window  | a 30 px bar with three dots and the app's name     | Maktaba Player (Windows, Linux)     |
| browser | a 30 px bar with the site's name only, no URL      | the web app, an install step's page |

- **How many:** home 8 (the hero's TV; the shelf's TV, phone, tablet, computer and browser;
  the English and Arabic pair), download 2 (the pair), an install guide one per step that
  needs one (the Synology mockup shows one), help and the problem pages none, legal none.
- **Backdrop:** a screenshot sits on the shot band (`site.surface.shot`), or on the hero with
  the brass glow behind it; never on a photograph or a gradient of its own.
- **Screenshots:** dark, real, on test data. The mockups use the devportal Apps screenshots
  as stand-ins (`web/packages/devportal/static/assets/apps/`); story 60.09 replaces them with
  the demo library. Never a household's library, never a drawn screen. Each has an alt text
  that says what the screen shows, written per language.
- **Language of a screenshot:** an Arabic page shows the Arabic screen and an English page the
  English one, except the pair, which shows both on purpose.
- **Icons:** one style, part A's: a 24-unit grid, a 1.75 stroke, round caps and joins, no
  fill, `currentColor`. Part B adds star, library, subtitles, live, updates, server, check,
  download, tv, phone, laptop, window, terminal, box, clock, lock, shield, chevron, play and
  memory (`P` in `mockups/part-b.mjs`).
- **Mirror rules:** icons that point along the reading direction mirror on an Arabic page
  (`.dir-icon`: onward, back, external link, the disclosure chevron). Everything else never
  mirrors: play (media runs left to right everywhere), check, download, the devices, clocks,
  the logo. Screenshots and frames carry `data-nomirror` and never flip; a screenshot's
  language comes from the screen itself.

## Part B layouts

Each at 360, 768 and 1280 px, English and Arabic, dark; home and download in light at 1280 too.

| Mockup                                    | Path                      | What it shows                                                                                               |
| ----------------------------------------- | ------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `home`                                    | `/`                       | the hero (the chosen tagline), the shelf of screens, six features, Arabic and English, privacy, three steps |
| `download`                                | `/download/`              | the visitor's platform first, then every system; other formats and verify in disclosures                    |
| `install-p3`                              | `/install/synology/`      | the Synology guide as the site's install page (Phase 3): facts, checklist, steps with a screen              |
| `help`, `help-topic`                      | `/help/`, `/help/search/` | the app's own help catalog and topic text, with the same filter as the problem index                        |
| `legal-p3`, `problem-p3`, `problems-p3`   | as in part A              | part A's pages in the part B shell (display `h1`, both-script footer)                                       |
| `wordmark`, `foundations`, `components-b` | sheets                    | the lockups; type, colour and budget; the components below                                                  |
| `social-home`, `social-download`          | the `og:image`s           | 1200 × 630 cards; `render.mjs` fails a title over three lines                                               |

Hidden on purpose: no card or link names the iPhone, iPad, Apple TV, Android or Android TV
apps, or a server package, while none is publicly installable (Q5, A7). The help index leaves
out those four topics (`HIDDEN_TOPICS` in `part-b.mjs`); `tests/mockups.test.mjs` fails a
part B page or copy file that names one.

## Part B components

Anatomy, states, sizes, RTL, and accessible name and role. All use logical properties only.

- **Download card** (`.card`, in a `ul.cards`): an icon ring, the system's name (`h3`), the
  requirements line, version, date and size (a `dl`), the primary button (an `a` with the
  file's real name in `download`), then two disclosures. The visitor's own system is the wide
  `.card.mine` at the top, with a brass edge and a "For this computer" badge; the platform note
  above says how it was guessed. States: rest, hover (edge to `site.accent.mark`), focus (part A's
  ring), disclosure open. Sizes: one column under 768, two at 768, the wide card plus three
  at 1280. Name: the `li` is labelled by its `h3`; the button's name is "Download for Mac", the
  size read after it.
- **Disclosure** (`details.disclose` / `summary`): "Other formats" and "Verify this
  download". The chevron mirrors in RTL and turns a quarter when open. The verify panel holds
  the digest (`.hash`, LTR, wraps anywhere) and the command (part A's code block, LTR).
- **The need block** (`.need`): "What do I need?", the home computer and the three kinds of
  screen, each with its icon and one line; a link to the install guides.
- **Feature block** (`.feature`): an icon ring (48 px), an `h3`, two sentences, one link
  "How … works" with the onward arrow. Six in a grid: one column, two at 768, three at 1280.
- **Device frame** (`figure.device`): the frame, the screenshot (`img`, real alt text,
  `loading="lazy"` except the hero's), and a `figcaption` with the screen's name and a line.
  `role` stays `figure`; the window and browser bars are `aria-hidden` (the caption names the
  screen).
- **Step with a screenshot** (part A's step list): the step's number ring, title and text,
  then a browser frame of the page that step shows.
- **Help layouts:** the index is part A's filtered index (a search field labelled "Filter
  topics", a live count, groups as `section`s labelled by their `h2`). A topic is a two-column
  layout at 1280 (a contents `nav`, then the topic's text as an `article` ending in related
  topics), one column below; app routes the site cannot link show as text, "In Maktaba:
  **Search**".
- **Social card** (`.card-canvas`, 1200 × 630): the both-script wordmark at the top, the
  title in display face (68/80 Latin, 72/108 Arabic, at most three lines), the arch at the
  inline end, and maktaba.tv with one line at the foot. It is an image: its text is repeated
  in `og:title` and `og:description`.

## Copy (story 60.02 part B)

`../copy/home.*.md`, `../copy/download.*.md` and `../copy/front-door.*.md` (the Phase 1
front door's lines, rewritten in the voice), each pair with the same keys; the mockups read
them directly, so the renders show the text under review. The tagline is in
`home.*.md` (`tagline` and its one-liner `tagline.says`): proposal 1 of the five each language
had, chosen on 2026-11-12 (Q1); `../copy/README.md` keeps the others as the record. Every claim is true of v0.3.131.
