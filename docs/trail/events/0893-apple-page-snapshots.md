---
schema: 1
kind: pr
pr: 893
title: The Apple Player's pages compared with references
date: 2026-11-07
session: 29e8f107-ee7f-4db2-a128-c02a2839240a
agent: claude-code
prs: [787, 813, 875]
---

## What happened
- Issue #876: the Apple Player had no layout references, so #787's empty panel stack (a gap on the library page) was found only by reading the code, while Android's Roborazzi goldens caught it at once.
- `PageSnapshots` in the hosted Mac Player tests draws the real library, series and video screens from MockHome's fixtures and compares each with a committed PNG. It covers no panel and "From plugins" collapsed and open, English and Arabic, and open at AX3: 24 references, with 0.2 % of pixels allowed to differ.
- Every drawn page is attached to its test, and `tools/apple/snapshots.py <run id>` copies a CI run's attachments into the references, so CI draws the references and a person reads them before committing.
- The first run had no references and failed by design. The references were committed from that run.

## What we learned
- The host app is sandboxed, so the references are read from the test bundle, never the source tree, and the drawn pages leave as attachments.
- The UI tests' MockHome already serves every browse route. Reusing it in the hosted test needed no new fixtures.
- Mac UI and hosted tests run only in CI (they open windows on the owner's screen). Locally, only `build-for-testing` was run, to check that it compiles.

## Keep doing
- When a layout bug is found by reading code, add the reference test that would have caught it.

## Open
- iPhone- and iPad-only layouts (compact width, the tab bar) have no references yet.
