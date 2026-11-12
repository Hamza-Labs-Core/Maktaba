---
schema: 1
kind: pr
pr: 1186
title: "maktaba.tv product screenshots of every app on an openly licensed demo library (60.09)"
date: 2026-11-12
session: 80fe98bd-cb5b-4c88-b529-064a938d631e
---

## What happened
- Story 60.09 merged as 5946ca851: `tests/fixtures/demo-library/` (Blender's six open movies
  and four Blender Studio shorts grouped by the fixture as a two-season series, each with its
  own art from its Blender Studio film page, English and Arabic titles and synopses written for
  the fixture, the Haddad family's home, three channels, a search with English and Arabic
  matches, a featured film), `routes.mjs` turning it into the home's answers per language in
  MockHome's route-table format, and captures of every app on it: the web (Vite and Playwright,
  `website / product shots (web)`), iPhone, iPad, Apple TV and Mac (`MockHome(demo:)`, shot
  tests exported by `tools/apple/export-shots.py` as `product-shots-*`), Android TV and phone
  (Roborazzi references under `src/test/screenshots/demo/`). `web/packages/website/scripts/
  shots.mjs --from-run` made the 46 images in `static/shots/` (WebP and AVIF at three widths,
  the largest file 79 KB) from CI run 43164926292.
- The Apple and Android parts were built by two children on separate branches merged into the
  pull request's branch; the fixture's route shape changed twice while they worked (every
  `/poster` 16:9, `portrait` for 2:3; guide slots on the quarter hour; a featured hero), each
  change sent to both.
- Problems hit and fixed on the way:
  - ffmpeg writes a `Lavc` comment segment into every JPEG; the art's no-metadata test caught
    it and the segments were stripped.
  - `npm install` of a package the lockfile already held (sharp, through wrangler) on macOS
    dropped two optional `@emnapi` entries; the lockfile was restored and only the root
    dependency line added.
  - `node --test <dir>` does not take a directory; the CI step names the test file.
  - `lint / repo` failed twice: `test_the_website_deploy_runs_on_the_website_filters_paths`
    requires the deploy's paths to equal the website filter (the fixture went into both), and
    the ci-paths guard found `product-shots.yml` in no filter (now in `apple_player_ui`).
  - The `Reference images` check run by the push read the pull request's body from before it
    listed the new images; the run the body edit started passed, and the stale run was re-run
    before merging, so every check's latest result was green.
  - The Apple child ended its turn mid-run with no report; sent back with its context, it
    fixed four defects its own images showed (a subtitle cue that ended before the shot, a
    subtitle playlist aligned with only the first video segment, a `switch` case that swallowed
    the demo playlist, an iPad length label that changed per run).
  - The Android phone's hero said "New series" for a film: the app labels an unpicked hero as
    the newest series, so the fixture's film is the household's pick.

## What we learned
- A demo fixture that serves the home's own answer shapes feeds every app with no
  app-specific copy: the web reads it through Playwright's routing, Apple and Android from the
  generated route files at test time.
- Look at the captures before fixing the fixture's shape: the letterboxed web cards, the
  "C…" guide cells and the hero's label were each visible only in a picture.
- A screenshot test needs a condition for what it shows (the subtitle's letters on screen, a
  poster's loaded tag), not a delay; the Apple subtitle check caught a missing line on runs
  that passed otherwise.
- Blender Studio's terms put its site's artwork under CC BY 4.0; its film list is drawn by
  script (no static links), and Caminandes 1 has no film page there.

## Keep doing
- Children for one story on separate branches merged into one pull request, each told at once
  when a shared fixture's shape changes.
- Re-run a check that read stale input rather than merging past its red entry.

## Stop doing
- Ending a child's brief without "do not end the turn until the report": the Apple child
  stopped on a running CI job.

## Open
- maktaba.dev's Apps pages could use these demo-library shots instead of their own test-data
  ones.
- No iPad Remote shot (no CI job runs the Remote's iPad UI tests).
- The Mac app's Arabic window does not mirror its layout and its sidebar truncates «الملف
  الشخصي»; Mac shots are 1024×674 (the runner's 1× display) and its player shows no controls.
- The Apple TV guide and the iPhone and iPad "On now" rows follow the simulator's clock, not
  the pinned noon.
- Android TV's player shot is the controls over the backdrop (Media3 cannot decode under
  Robolectric): no subtitle line, and Media3's controls do not mirror in Arabic.
- 60.01 part B sets the frames and widths (`shots.config.json`); 60.10 and 60.11 place the
  images (T04, T05).
