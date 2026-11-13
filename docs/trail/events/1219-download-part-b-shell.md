---
schema: 1
kind: pr
pr: 1219
title: "maktaba.tv's download page in part B's shell"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1219 merged as 3a4cbf91e: the download page (60.11) moved from part A's shell to part B's,
  as the home page and the approved mockup: the wordmark lockups, the Phase 2 nav, the display
  face's preloads, part B's stylesheet and part B's social card (`CARD_SUB` has `/download/`).
  Its own stylesheet is `src/download.css` alone, since part B's comes with the shell.
- CI's renders caught that the page's own nav still sent Download to the release on GitHub:
  while the download family builds, `/download/` is not written, so `bLinks` could not see it.
  The page counts its own path as built, and `currentOf()` knows `/download/` (and `/help/` for
  60.12), so the nav marks the page current. A build test checks both languages.
- The reference images were listed in the body before they were pushed; the reference-images
  check passed on its first run. `design/review-phase-2.md`'s shell and card rows are fixed.

## What we learned
- A page that decides its links from what the build has written must count itself: it is
  never written before its own links are chosen.

## Keep doing
- Look at CI's first render of a moved page before taking it as the reference.

## Stop doing
- Asking the build's resolver about the page being built.

## Open
- Before Phase 2 is done: the owner's read of the Arabic copy (Q2, A5), and the captures held
  back by #1198 and #1199.
