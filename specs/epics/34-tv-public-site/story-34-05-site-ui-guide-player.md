# Story 34.5 — Site UI: guide and player

## Description

*As a visitor on my phone or laptop, in Arabic or English, I want to see what is on every channel now,
browse the next days, and watch with one tap; I want to share a channel or a programme and embed the
player on my own page when the owner allows it.*

Design: [design.md](design.md) §3.3, §6.1; README R5.

## Acceptance criteria

- **AC1** Pages: home (channel grid: logo, name, number, now with progress, next), guide (EPG: channels ×
  time, `guide_days`, "now" line, jump to now, day picker), watch `/<lang>/watch/<slug>[?at=]` (player,
  now/next, today's schedule), embed `/embed/<slug>`, about, legal (terms, privacy, copyright/takedown).
- **AC2** Mobile-first: usable at 360 px, grid becomes a list on narrow screens, the guide scrolls
  horizontally in time with sticky channel column; Lighthouse mobile performance ≥ 90 on the home page
  (guide JSON excluded).
- **AC3** Arabic is the default with `dir="rtl"`; English `ltr`; language from the URL prefix, then
  `Accept-Language`; a language switch keeps the current page; the time axis of the guide runs in the
  reading direction (right-to-left in Arabic).
- **AC4** States: live, slate ("not available online" chip), tuning card, off air (home offline,
  budget, capacity, channel), each with a clear message from `site.json` texts.
- **AC5** Share: Web Share API on mobile, copy link elsewhere, for channels and programmes (`?at=`);
  OG image = channel logo on the brand background.
- **AC6** Embed: `/embed/<slug>` renders only the player and a link to the site; `frame-ancestors`
  allows only `embed_origins` (none by default → embedding refused).
- **AC7** SEO: title/description/OG/`hreflang`/canonical/JSON-LD (`TelevisionChannel`, `BroadcastEvent`
  for now/next) per page; `sitemap.xml` lists channel pages in both languages; `robots.txt` and
  `<meta name="robots">` follow `indexable` (default `noindex`).
- **AC8** Accessibility: axe clean, keyboard reachable, focus visible, guide cells are links with the
  programme title, time and channel in their accessible name; captions not required (live).
- **AC9** Every link points to the site (no home/relay hosts) — enforced by the 34.2 scan.
- **AC10** Times in the viewer's zone (with the channel's zone on hover/long press), 12/24 h by locale,
  Arabic-Indic digits optional by setting (`branding.arabic_digits`, default off).

## Test cases

- **TC1** `Grid.test.tsx`, `Guide.test.tsx` (RTL and LTR snapshots, now line, day switch).
- **TC2** `Watch.test.tsx` (`?at=` live / future / past).
- **TC3** Playwright `site-mobile.spec.ts` (Pixel/iPhone viewports, ar and en), `site-a11y.spec.ts` (axe).
- **TC4** Worker `html-meta.test.ts` (meta and JSON-LD per route), `embed-csp.test.ts`.

## Edge cases

- **EC1** A channel removed from the lineup while open: the page shows "This channel is no longer
  online" and offers the grid.
- **EC2** Very long titles: two lines then ellipsis; full title in the details sheet.
- **EC3** Guide with no data beyond today (`guide_days = 1`): "Guide ends here" marker.

## Dependencies

34.2, 34.4, 34.6 (player module), 34.8 (branding).
