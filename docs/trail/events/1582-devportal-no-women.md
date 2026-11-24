---
schema: 1
kind: pr
pr: 1582
title: "maktaba.dev's screenshots, and the test data behind every app's screens, show no woman or girl"
date: 2026-11-24
session: a0368eb0-9e83-4dbf-8f4e-e64674284368
agent: claude-code
prs: [1582, 1565]
---

## What happened
- **The check:** every image on maktaba.dev (156) and every image committed under `apps/`, `docs/` and the two sites (915) went through two passes. An OCR pass used macOS Vision (English and Arabic) through a small Swift script; every devportal image was also looked at on contact sheets.
- **What it found:**
  - Fire TV's home and library showed Sintel, from the old demo library.
  - The big screen's and Roku's search filters listed the speakers "Maryam" and "Rania".
  - The web app's search shots were signed in as "sara" and "amina".
  - The Android Player's profile references showed the fixture user "layla".
- **The fix in #1582:**
  - Every client UI-test fixture that named a woman now names a man (19 files: Apple, tvOS, Android, Android TV, desktop, Roku, the /tv/ mock).
  - The website's web capture (CI's `product-shots-web`) also takes the search box's suggestions and AI mode's answer.
  - The 14 maktaba.dev images were rebuilt from CI output: Fire TV references, `tv-web-logs`, `roku-logs`, `product-shots-web` (run 45328154156).
- **Reference images:**
  - The Roku search-filter references were replaced with the CI run's screens.
  - The four Android profile references were deleted so verify failed and wrote them (run 45331146241).
- **Main dropped the Fire TV shots:** while #1582 was in CI, main dropped maktaba.dev's two old-library Fire TV shots (fd098a846). Merging #1565's branch into #1582 kept that deletion, so 10 images were rebuilt, not 14.
- **CI on a stacked PR:** CI runs only for pull requests to `main`, so #1582, stacked on #1565, targets `main`. Changing the base does not start CI (`edited` is not one of ci.yml's events); an empty commit did.

## What we learned
- **Loose thresholds hide renamed text.** Roborazzi's 2% threshold and Roku's mean-difference tolerance both passed screenshots whose only change was a name. A renamed fixture needs its references rewritten on purpose: delete the Roborazzi reference so verify writes it, or copy the Roku screen from CI's `build/screens`.
- **OCR finds names in images cheaply.** VNRecognizeTextRequest found every woman's name that had been seen by eye, plus the "layla" references nobody had looked for.
- **maktaba.dev's web search shots had no committed capture.** They were taken with a scratch script; now they come from `capture-web.mjs` in CI, like the site's.

## Keep doing
- Running the OCR scan over committed images whenever demo or test data changes, and listing what it finds in the PR.

## Stop doing
- Naming a test fixture after a woman, in any test whose screen can end up in a picture.

## Open
- **Women's names that never reach a screen:** Go and API tests, web unit tests (jsdom), the Android client's and MaktabaKit's unit tests (sara, amina, huda, layla, "Grandma's" as a home nickname). They were left as they are, and could be renamed in a sweep.
- **Cloud admin mockups:** `web/mockups/cloud/*.html` name "Sara Kassem". These are design mockups, not app screenshots.
- **Merge order:** #1582 includes #1565's commits until #1565 merges, so #1565 goes first.
