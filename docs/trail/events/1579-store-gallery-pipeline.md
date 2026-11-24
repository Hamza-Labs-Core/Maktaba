---
schema: 1
kind: pr
pr: 1579
title: "Store galleries (60.14 part 2): the composer and the workflow"
date: 2026-11-24
session: e1c12b82-6431-40f4-b0ae-96e43fdfa26e
agent: claude-code
---

## What happened
- The six part 1 decisions were approved on 2026-11-24, as proposed, and story 60.14 records
  them (#1566's head d6a84f851).
- #1566's `lint / lint` failure was its own: Prettier had not run over the gallery README,
  which was written after the format pass.
- compose.mjs draws all 260 images of the 13 listings from captures named
  `<surface>-<screen>-<lang>.png`, in the approved style. Each image is:
  - written as PNG, else the best JPEG under the store's limit;
  - flattened where alpha is refused;
  - checked against stores.json;
  - laid out for fastlane deliver and supply, with a contact sheet, report.json and a
    checklist for the no-women review.
- store-gallery.yml runs it by hand on a run's `*-shots-*` artifacts and uploads
  store-gallery-<sha>. Its test runs in a new website job in the Playwright image.
- `lint / repo` failed twice on the workflow:
  - every artifact download must be the three-attempt chain (test_artifact_download);
  - the website filter must equal the website deploy's trigger paths. The workflow is now
    in NOT_IN_CI as run by hand.

## What we learned
- **A workflow run only by hand** belongs in ci_paths.py's NOT_IN_CI with a reason, not in a
  filter: the website filter is mirrored by website-deploy.yml.
- **fastlane supply reads Google Play's listing per locale,** so the single icon is written
  into each locale's folder.

## Open
- 18 of the 92 screens the set needs are captured today. The shot tests for the rest come on
  the new demo library (#1565): Apple, Android phone and tablets, the Remote, Roku, and the TV
  web UI for Samsung and LG.
- Then: run store-gallery.yml, look at every image, and fill in the checklist in that pull
  request.
