---
schema: 1
kind: pr
pr: 1209
title: "Epic 60 story 60.11: maktaba.tv's download page, and the CI gaps its references found"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1209 merged as 0d3e7185f: `/download/` and `/ar/download/`. `scripts/download.mjs` reads the
  latest stable release from the GitHub Releases API at build time (assets matched by pattern,
  digests checked against `checksums.txt`), `src/downloads.json` holds one entry per platform; a
  platform with no asset and no public listing is not shown (no phone or TV card: Q5, A6; no
  package rows: A7). The visitor's platform card comes first, chosen before the first paint;
  each card has its version, date, size, other formats and a "Verify this download" disclosure.
  The release workflow dispatches the website deploy after a stable release (still gated by
  `vars.WEBSITE_LIVE`), and the release notes point to the page.
- Problems found and fixed on the way, none by loosening a test:
  - The visual check redraws each card with a saved release's numbers and compared the cards in
    order; the page puts the visitor's platform first (CI's browser reads as Windows). It now
    compares the set, still failing on a missing, extra or duplicate card.
  - CodeQL: `plain()` stripped tags in one pass, then decoded `&lt;`; it now strips until none
    is left and drops any stray `<` or `>`.
  - A page with brand-new references could not get them: Docker is off on the Mac, and with
    `updateSnapshots: "none"` CI wrote no image for a missing reference. CI now writes it in the
    runner's image, uploads it (`website-new-references-<shard>`) and fails the job.
  - That step first found the new files with `git status`, which printed nothing in the job's
    container (git refused the checkout's ownership), so the job passed with eight references
    written. It now compares file times against a marker set before the run and fails when the
    marker is missing.
  - Late pictures were left black in full-page shots; the references mask the apps' screenshots,
    the same change as #1208.
  - After #1208 merged, five files conflicted. The merge also left two `display` entries in the
    shell's assets (one by script, one by file name); the second won and the home page preloaded
    `href="undefined"`. The built-site link check caught it in CI; the local run had left out
    `--dist`. One table now, read by script.
  - With `/download/` built, part B's shell sends the home page's Download links there, so six
    home references changed and were re-taken from CI's renders.
  - Two runs of the reference-images check on one head again: the push's run read the body
    before the images were listed and failed; the edit's run passed. Re-running the stale run
    read the body fresh and passed.

## What we learned
- A check that discovers files must fail closed when it cannot see: an empty answer from a
  command that may have failed is not "nothing to report".
- New pages need a path for new references that does not depend on a Mac or Docker.

## Keep doing
- Run the link check against the built site (`links.mjs --dist dist`) before pushing a change
  that touches what pages link or preload.

## Stop doing
- Pushing a branch and editing its pull request's body as two steps: list the reference images
  in the body before pushing them, or re-run the check after.

## Open
- The download page is in part A's shell; moving it to part B's shell and social card is a
  follow-up.
- Store badges wait for public listings (A6); package rows for A7; the "How to install" links
  for 60.12's help topics. The new Arabic strings wait for the owner's read (Q2).
