---
schema: 1
kind: pr
pr: 1208
title: "Epic 60 story 60.10: maktaba.tv's home page"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1208 merged as b9d587931: the home page at `/` and `/ar/`, replacing the Phase 1 front door.
  Part B's approved layout and copy, the chosen tagline as the headline, the demo-library
  screenshots (60.09) in device frames with `srcset`, width and height (only the hero preloaded),
  a Download button set to the visitor's platform before the first paint, part B's shell
  (wordmark lockups, the display face) on this page, JSON-LD `Organization`, the social card.
  Lighthouse mobile 97/100/100/100 (en) and 99/100/100/100 (ar), CLS 0.
- The visual references mask the apps' screenshots in the frame's near-black: with eight
  photographs they were 1.3 to 1.5 MB, over the 1 MB a reference may be, and a release's new
  screenshots would otherwise change every reference. The pictures are checked by
  `scripts/shots.test.mjs` and 60.09's T04 instead.
- It sat red for hours on a CANCELLED run of the reference-images check (see #1211's event).
  After #1207 merged it went CONFLICTING: both changed the front door (#1207 its headline and
  references, #1208 removed it). Main was merged in keeping the home page; #1207's build test
  then read the home page's headline, the same tagline.
- #1209 (the download page) took #1208's screenshot-mask change word for word, expected to merge
  cleanly whichever landed first. It did not: #1208 also changed `scripts/shell.mjs` (part B's
  shell options), `scripts/lint-css.mjs`, the package README and created
  `design/review-phase-2.md`, all of which #1209 changed too; the identical hunk merged, the
  files around it did not.

## What we learned
- Two pull requests that each start a shared file (here the Phase 2 review) and each extend the
  shell's options conflict whatever one copies from the other; copying one hunk does not make
  two branches merge cleanly.
- With 60.11's page built, part B's shell sends every Download link to `/download/` on its own,
  so the home page's references change when the download page lands.

## Keep doing
- Pick the cleanest capture for each frame, and say what the captures got wrong (#1198, #1199).

## Stop doing
- Promising that two open pull requests on the same package merge in either order.

## Open
- The phone frame is captioned "In the phone's browser" but shows the iPhone app (not public,
  Q5): replace it with a capture of the web app at phone width.
- A test that each page's screenshots are in its own language (the references mask them).
- The English headline wraps to four lines on Linux at 1280 px (three in the mockup).
- `design/review-phase-2.md` to be signed by the lead; the owner's read of the Arabic copy (Q2).
