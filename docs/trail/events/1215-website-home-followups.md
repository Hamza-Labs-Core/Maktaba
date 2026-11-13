---
schema: 1
kind: pr
pr: 1215
title: "maktaba.tv: the phone frame shows the web app, screenshots checked per language, the tagline in three lines"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1215 merged as ae002c5d9, three follow-ups to the home page (#1208):
  - The phone frame was captioned "In the phone's browser" but showed the iPhone app, which is
    not publicly installable (Q5). `capture-web.mjs` now also takes the web app's home at a
    phone's width (393 × 852) as `webphone-home-<lang>`, processed from the PR's own CI run, and
    the frame shows it.
  - The visual references mask the screenshots, so a build test checks each page's screenshots
    are in its language, with the pair and the English Mac window (#1198) as the only
    exceptions.
  - The English tagline broke into four lines at 1280 px on Linux. Lifting its 16ch cap changed
    nothing: the 433 px text column was the limit, and the display face runs a few pixels wider
    on Linux than on a Mac. The column is now 5.5:6.5 (477 px).
- Two bugs found and fixed on the way: at a phone's width the web app's lazy images never loaded,
  so the capture waited forever (it now loads them eagerly and fails after 15 s naming them); and
  `shots --only <app>` rewrote the manifest with that app alone, which would have dropped 46
  entries (an unprocessed app now keeps its entries, with a test).
- The reference images were listed in the body before they were pushed, and the
  reference-images check passed on the first run.

## What we learned
- Measure the box before changing a measure: the cap was not what wrapped the line.
- A capture that waits for every image needs a deadline and must make lazy images load, or a
  narrower viewport hangs it.

## Keep doing
- List reference images in the body before pushing them.

## Stop doing
- Writing a whole manifest from a partial run.

## Open
- The download page moves to part B's shell next.
