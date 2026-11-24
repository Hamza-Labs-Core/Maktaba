---
schema: 1
kind: pr
pr: 1565
title: "The demo library behind every maktaba.tv screenshot is now documentaries and animal animation, with no woman or girl in any picture"
date: 2026-11-24
session: a0368eb0-9e83-4dbf-8f4e-e64674284368
agent: claude-code
prs: [1565]
---

## What happened
- **The old library broke the rule.** Story 60.09's demo library (`tests/fixtures/demo-library/`) was Blender's open movies, and several have women or girls in their art and frames: Sintel, Spring, Tears of Steel, Coffee Run, Sprite Fright. Sintel was the film opened on every film page and in every player shot, and the player's stream was a still of Sintel's art (`apps/apple/UITests/Fixtures/Stream/demo-picture-*.ts`).
- **New library, same source.** #1565 replaces it in the same place: six documentaries with no people in them and four films whose characters are all animals.
  - The documentaries are NASA/JPL's Curiosity animation, Apollo 11's launch from camera E-8, Along the Rings of Saturn (CC BY 2.0), Lake Nasser from Space, the Moon's phases for 2023, and NOAA's ghost octopus.
  - The animal films are Big Buck Bunny and the three Caminandes films.
  - Each licence was read from the film's Commons or Blender Studio page.
  - The new art and the player's still are frames of the films themselves.
- **CI's runners regenerated every screenshot; nothing ran on a local Mac.**
  - Web and the Apple apps: the `product-shots-*` artifacts of run 45306789661.
  - Android phone and TV: the actual images Roborazzi wrote in runs 45306789661, 45312070295 and 45313275908.
  - `npm run shots` then remade the 288 files in `static/shots/`.
- **A unit failure on the way:** `unit / unit` failed in run 45306789661 because the maktaba.tv mockups quote the home page's alt texts and were stale (`design/tests/mockups.test.mjs`). `node design/mockups/make.mjs` fixed it.

## What we learned
- **Look at the whole film, not only its poster.** A contact sheet (48 frames over the whole film, `ffmpeg … fps=1/N,tile=8x6`) checks a film in one image. The player's still and every card come from the library, so checking the library at its source covers every screenshot built on it.
- **Roborazzi's 2% threshold can hide changed content.** The Android TV guide passed verify on the old picture because only its text changed. Deleting a reference makes verify fail and write an `_actual.png`, which is how CI produced the new guide.
- **Some reference images come only after a step passes.** The Fire TV references come from a step that runs only when the Google build's step passes, so they took a run of their own.
- **The visual references don't change.** maktaba.tv's visual references mask the product shots (`SHOT_MASK`), so a change to the demo library leaves them as they are. Only `static/shots/` needs listing under `Reference images:`.
- **Wikimedia's API is rate-limited.** It answers 429 to parallel requests and to a generic User-Agent. One request at a time, with a User-Agent that names the project, works.

## Keep doing
- Check every picture by eye before it ships, and list each one in the PR with what it shows.
- Take a film's art from the film's own frames, or from its maker's openly licensed card, and record where each came from.

## Stop doing
- Picking demo films by their licence alone. The content rule (no woman or girl, family-appropriate) is checked first, frame by frame.

## Open
- **Arabic-language documentary:** none that is openly licensed and shows no woman was found. One that is would replace a title (the fixture's README, "Arabic content").
- **Gran Dillama's licence:** Commons' reviewed licence is CC BY-SA 3.0. The fixture records that, not the CC BY 3.0 it recorded before.
- **Other test data with people:** maktaba.dev's app screenshots and `deploy/review-home/content/` (Sintel, Spring, Cosmos Laundromat) use other test data. They were not part of this change and still need the same check.
