---
schema: 1
kind: pr
pr: 1291
title: "Epic 58 story 58.10: Maktaba for Samsung TVs (AVPlay, the .wgt, the Samsung store)"
date: 2026-11-14
session: 0c3e16d1-4344-4763-a698-1cb3e76a4e60
agent: claude-code
---

## What happened
- #1291 merged as 86d8a98ae, after #1290 (LG), whose shared commits it also carried.
- `apps/tv/web/src/platform/tizen/avplay.ts`: the AVPlay adapter (HLS, MP4, MKV, TS; 4K by
  `ADAPTIVE_INFO` with `SET_MODE_4K` as fallback; seeking, audio tracks, subtitles; suspend and
  restore with the app switcher; AVPlay errors become `state.error`).
- `caps.ts` maps the TV to 58.02's `device_caps` from productinfo, decoder MIME support, avinfo
  HDR and the `tvaudiocontrol` output mode; it never lists Dolby Vision or HDR10+, and is
  tested against the Samsung vectors.
- `apps/tv/web/tizen/`: `config.xml` (Tizen 5.5+, privileges, CSP), `package_wgt.py`
  (reproducible unsigned `.wgt`), `lint_wgt.py` (`--signed` too), `sign_wgt.sh` (Tizen CLI 6.1
  pinned by sha256; without certificates it rehearses with a throwaway one).
- CI: `_tv-web.yml` builds, packages and lints on every PR; `tv-web-release.yml` signs on a
  `v*` tag whose launcher changed and attaches the package only when signed with the
  project's certificates.
- `docs/stores/samsung.md` (listing en/ar, Public Seller then Partner path, signing,
  submission, 13-item real-device checklist), maktaba.dev's Apps page and the launcher guide in
  en and ar, CHANGELOG in both.
- Tests: Vitest (AVPlay against a fake, caps vectors), 16 package tests, Playwright
  `tizen-avplay.ui.spec.ts` on a `webapis`/AVPlay shim; signing proven in ubuntu:24.04.

## What we learned
- AVPlay's suspended flag must reset on stop, or the next video does not suspend.
- Release secrets belong to the one step that uses them (Sign), not the job.
- The link check reads a Samsung widget id as a link: it needed marking as an id.
- The Samsung TV emulator (58.16) is not worth a CI run: it needs a KVM runner and the TV
  extension, has no headless mode, and has no AVPlay or UHD, so it shows less than the shim.
- A merge-order note on the PR (what main changed, which job to re-add) let the second
  platform PR take main in without guessing.

## Keep doing
- Rehearse signing with a throwaway certificate in CI so the path is proven before the owner's
  certificates exist; never attach a rehearsal build.

## Open
- Owner: a Samsung Seller Office account (Public Seller, US launch); Partner Seller status for
  other countries, the Gulf included.
- Owner: author and distributor certificates, and the release secrets
  `TIZEN_AUTHOR_P12_BASE64`, `TIZEN_AUTHOR_PASSWORD`, `TIZEN_DISTRIBUTOR_P12_BASE64`,
  `TIZEN_DISTRIBUTOR_PASSWORD`.
- Owner: confirm or replace the package ID `HLMaktaba1`; submit in Seller Office; run the
  real-device checklist on 2020, 2022 and 2025 TVs.
- This event was written after the merge by a separate session; no session event existed.
