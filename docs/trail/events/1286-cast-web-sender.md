---
schema: 1
kind: pr
pr: 1286
title: "Epic 58 story 58.06, part 1: the shared Cast sender contract and the web sender"
date: 2026-11-14
session: 7e5507e4-d290-4a3c-8efc-787b32a030c0
agent: claude-code
---

## What happened
- #1286 merged as 52faf72aa. It adds `shared/cast/`:
  - `app.json`: the receiver's application ID (empty until the Cast console registration), its
    URL and the custom namespace;
  - `load-vectors.json`: the load's custom data case by case, which the web, Android and Apple
    senders each run.
- The web player gets **Cast to a TV** (`web/src/lib/cast/`, `components/CastControl.tsx`). It
  shows only in Chrome, on an HTTPS page, outside Maktaba Cloud, with an ID set and a home
  address a TV can reach. The sender mints a cast grant, loads it on Maktaba's receiver
  (#1267), follows the media status (the queue's next video, the position) and renews the grant
  over the namespace 30 minutes before it ends.
- vite reads the ID at build time, and both Dockerfiles copy `shared/cast/app.json`. The grant
  API's refusals got problem entries (`cast-via-cloud`, `cast-needs-person`, `cast-scope`),
  which the web's registry test required. `shared/cast/**` runs the web, Android and Apple jobs.
  The casting guide gained a senders section, and `docs/stores/cast.md` gained real-device rows
  16 to 21.
- The story was split per platform: Apple is #1287 and Android is #1292, stacked on this
  branch, then retargeted to main once it merged.
- On the way:
  - the `website` job wanted `dist-manifest.json` rebuilt for the three new problem pages;
  - `affected-tests` wanted a test change for `VideoPlayer.tsx`;
  - four merges of main conflicted in the Dockerfiles, `vite.config.ts`, `VideoPlayer.tsx` and
    `ci-paths.yml`, as 58.09, 58.11 and the Fire TV work landed beside it.

## What we learned
- A Python resolution script that asserts after writing some files and before others leaves a
  merge half-resolved. `git add` then committed conflict markers in two Dockerfiles. Check
  `git grep '^<<<<<<< '` before finishing a merge, and amend before pushing.
- `castData` in the receiver (#1267) plays a load without Maktaba custom data as it came. That
  gives an Apple sender a fallback for an older home without switching app IDs, which iOS
  allows only at setup. Android has `CastContext.setReceiverApplicationId` and uses Google's
  Default Media Receiver instead.
- The web is served by the home itself, so a web sender never meets a home older than the
  grant API. Only the native apps need the older-home path.
- A new problem entry changes the website's page list: rebuild `web/packages/website` and
  commit `dist-manifest.json` in the same push.

## Keep doing
- Put a cross-platform rule in one shared vectors file and have every platform's tests run it
  before the platforms are written.
- Merge main into a stacked parent PR as soon as its watcher reports CONFLICTING; main moved
  four times in one afternoon.

## Stop doing
- Resolving conflicts with a script whose assertions sit between file writes.

## Open
- The receiver's application ID: the owner registers the receiver in the Cast console and
  writes the ID to `shared/cast/app.json` (then `tools/apple/cast-app-id.py`). Until then no
  build shows Cast, and the Android Player no longer casts to Google's Default Media Receiver.
- #1287 (Apple) and #1292 (Android) are open.
- Real-device rows 1 to 21 in `docs/stores/cast.md`.
