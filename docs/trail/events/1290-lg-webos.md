---
schema: 1
kind: pr
pr: 1290
title: "Epic 58 story 58.11: the LG webOS app, the .ipk, native playback and subtitles"
date: 2026-11-14
session: 0c3e16d1-4344-4763-a698-1cb3e76a4e60
agent: claude-code
---

## What happened
- #1290 merged as b2feabc96. An LG TV on webOS 5 or later runs Maktaba: the TV's own player
  (native `<video>`, no hls.js/MSE) with audio tracks, 4K, HDR10/HLG and Dolby passthrough;
  Dolby Vision only on model years in `DV_GOOD_YEARS`.
- Device caps come from luna `getSystemInfo`/`webOS.deviceInfo` plus `isTypeSupported`,
  tested against `caps-vectors.json` (`webos-2022-c2`). Luna calls use documented services
  only, declared in `appinfo.json`'s `requiredACG`. Back (461) maps to `platformBack`.
- Shared with Samsung (58.10, #1291), in separate commits that both branches carried:
  the subtitle layer (`apps/tv/web/src/subtitles/`: WebVTT, RTL, scales), `audio_tracks` in
  `maktaba:state`, `DeviceCaps`/`Platform.player`, and /tv/ playing through the launcher
  (`web/src/bigscreen/input/launcherMedia.ts`): stream sessions send `client_profile` and
  `device_caps`, and an error retries once with `force_transcode`.
- The `.ipk` (`apps/tv/web/webos/`): pinned `ares-package`, unsigned (LG signs on
  submission), and `lint-ipk.mjs` opens the built package. `_tv-web.yml` builds and lints it;
  `tv-web-release.yml` attaches it with its sha256 to a `v*` tag whose launcher changed.
- `docs/stores/lg.md` (listing en/ar, UX scenario, self-checklist, real-device checklist), the
  launcher guide and maktaba.dev's TV page in en and ar, CHANGELOG in both.
- Tests: Vitest (webOS, subtitles, launcherMedia), the package tests, and Playwright
  `webos.ui.spec.ts` on a webOS shim.

## What we learned
- Playwright's Chromium decodes no H.264, so the CI flow plays VP9; only a device proves HLS,
  HDR, DV and passthrough.
- The webOS TV Simulator cannot run in CI: a license-gated download with no fetchable URL, no
  documented headless mode, no media pipeline.
- The web and cloud images had to copy `shared/playback/caps-vectors.json` once the bigscreen
  tests read it.
- An escaped tag inside a subtitle cue must show as text, never become an element.
- Two platform PRs sharing commits merge cleanly only in a fixed order: #1290 first, then #1291
  re-adds its job beside `webos-ipk` after taking main in.

## Keep doing
- Put code two platforms share in its own commits on a shared branch both merge.
- Pin every third-party action by sha, the new release workflow included.

## Open
- Owner: an LG Seller Lounge Individual Seller account; register `com.hamzalabs.maktaba.tv`;
  submit the `.ipk`, store images, listing, UX doc, self-checklist and review-home sign-in.
- Owner: run the real-device checklist on webOS 5/6/current (and 4.5 for check 11), then add
  proven years to `DV_GOOD_YEARS`.
- This event was written after the merge by a separate session; no session event existed.
