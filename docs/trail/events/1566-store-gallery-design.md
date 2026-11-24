---
schema: 1
kind: pr
pr: 1566
title: "Store gallery images (60.14 part 1): the style, the beats and a size manifest, as mockups for review"
date: 2026-11-24
session: e1c12b82-6431-40f4-b0ae-96e43fdfa26e
agent: claude-code
---

## What happened
- Story 60.14 opened as a design pull request (head 2a95f2259), kept open for review. It
  contains:
  - `web/packages/website/design/store-gallery/stores.json`: every store's image sizes, counts,
    formats and limits, with each store's own source;
  - `beats.json`: 7 Player, 6 Remote and 7 TV beats, the captions in English and Arabic;
  - a captioned style with no device frame;
  - 22 mockup renders and tests in `make test-tokens`.

  The full set waits for approval and for the new demo library.
- The 13 listings were checked against the release workflows:
  - six Apple listings (`testflight.yml`);
  - three Google Play listings (`play-bundles.yml`);
  - Fire TV, Roku, Samsung and LG.

  Microsoft Store, Flathub and Snap have nothing packaged, so they get no images.
- The first renders used maktaba.dev's `firetv-home` and `firetv-library` screenshots as
  stand-ins. Those were taken on the old demo library and show Sintel's poster. The TV sheet
  showed it, and the stand-ins were swapped before the first push. Two other screens carry a
  woman's name: the Android Profile reference ("layla") and the web search screen's signed-in
  user. The test now refuses `demo/` references and the two Fire TV screenshots.

## What we learned
- **Store rules decide the frame.** Google Play refuses device imagery and caps taglines at 20%
  of the image. Apple's marketing rules allow only Apple's own bezels, unmodified. Neither
  fits part B's generic frame, so the gallery puts the screen in a plain panel. The render
  measures each caption's text area against the 20% cap.
- **The TV stores want the app as it is:**
  - Google TV requires an unaltered screenshot (TV-G4);
  - Amazon asks for only the app;
  - Samsung's FAQ says icons and screenshots cannot differ per language, and LG's guide
    describes one set too.

  So one TV set serves every language there, with the Arabic screen second (Samsung hides
  number 3 on some TVs).
- **Apple's required iPhone size** is now the 6.3" Dynamic Island class (1206 × 2622), not
  6.9". iPhone Duo screenshots become required in April 2027 for apps built with the iOS 27.1
  SDK.
- **"Test data" is not the same as "no people".** maktaba.dev's screenshot set mixes
  test-data screens with demo-library ones, and test fixtures carry a woman's name. Each
  stand-in had to be looked at, at full size where a name is small.
- **Roku's docs** answer under `developer.roku.com/dev/docs/<page>`, and appending `.md` gives
  the markdown. WebFetch got a 403 there and curl worked. LG's Seller Lounge needs a login, so
  its screenshot counts come from a 2021 guide and are marked unconfirmed.

## Keep doing
- Measure a store's text rule in the render (caption share, line counts) instead of judging it
  by eye.
- Look at every stand-in at full size before using it, names included, and refuse the known
  bad sources in a test.

## Stop doing
- Treating maktaba.dev's Apps screenshots as safe demo content as a set: some of them show the
  old demo library.

## Open
- Owner approval of the style, beats and captions (60.14 part 1). Then part 2:
  - new UI-test screens for each beat marked `new` in `beats.json`;
  - `compose.mjs` and `store-gallery.yml`;
  - the full set on the new demo library, with a per-image check.
- maktaba.dev's `firetv-home` and `firetv-library` screenshots still show the old demo library
  on the portal.
- The Android Remote's attention stand-in names Plex. Third-party names are to stay out of the
  full set's captures (Google Play's metadata policy).
