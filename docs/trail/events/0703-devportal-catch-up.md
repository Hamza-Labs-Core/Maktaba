---
schema: 1
kind: pr
pr: 703
title: maktaba.dev Apps pages catch up with Epics 52, 53, 55 and 56, and the portal-follows rule
date: 2026-11-06
session: a9659d13-25bf-46c0-a91b-5c78cc44b65e
---

## What happened
- The Apps pages had not changed since #623/#626 (2026-11-03). They now cover the Windows and Linux Player, the TV library view and play queue, word review, the status strip, the Logs, Errors and Dashboards pages, the recovery page, safe mode and `serve --offline`, in English and Arabic, with 16 new screenshots on invented data. Report a problem (#691, open) is marked "coming in the next release".
- CLAUDE.md rule 7: a PR that ships user-facing work updates maktaba.dev in the same PR. `tools/ci/devportal_follows.py` warns in `affected-tests` when one does not; `No-Portal-Change:` silences it.

## What we learned
- The desktop Player can be shot with no display: `runDesktopComposeUiTest(width, height)` on `TestHome`, then `onRoot().captureToImage().toAwtImage()`. Wrap `Shell` in the window's `Surface(color = background)`: without it the page area is white.
- The web app can be shot without a stack: `vite` plus `playwright-core` driving the installed Google Chrome, every `/api/*` and `/status.json` answered from a route table. Set `mkt:usage:consent` (granted false) in local storage first, or the usage-data prompt covers the top of every page.
- CI's `tv-ui-tests` artifact (from a merge run on main) already holds dated Apple TV shots on MockHome (`tv-shots/*.png`, 3840×2160). The Shuffle one is a black playing frame.
- The devportal build lazy-loads screenshots: a full-page capture shows them blank. Scroll each one into view to check it.
- Replaying the guard over the last 40 merges (`M^1...M^2`) flagged only the feature PRs. That was the check that a warning would not be noise.

## Open
- Shots of the Remote's word review and the phone Players' queue are not taken (text only). The Android Roborazzi tests have no reference for those screens yet.
