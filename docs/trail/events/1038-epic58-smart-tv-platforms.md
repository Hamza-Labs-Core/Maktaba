---
schema: 1
kind: decision
pr: 1038
title: "Epic 58 spec: apps for the TVs Maktaba does not reach yet (Fire TV, Google Cast, Samsung, LG, Roku)"
date: 2026-11-08
session: 2561424c-1eb5-40bf-9cb1-e8cab43bb9ce
agent: claude-code
---

## What happened
- Spec only: 18 stories, research by platform, the Arabic store and the wiki page.
- **Order:**
  1. Fire TV, as an `amazon` flavor of the Android TV app;
  2. Google Cast, as a Maktaba custom receiver at `cast.maktaba.tv` plus senders in both Players
     and Chrome;
  3. Samsung and LG, on one web-TV launcher that hosts the home's own `/tv/` UI;
  4. Roku, native, with Arabic shaped by the app;
  5. Vega OS, gated on its share.
- **Out of scope:** Xbox, VIDAA and Vizio's own platforms (reached by Cast and AirPlay instead),
  TVs before 2020, and DRM.
- **Shared groundwork:**
  - an API level and `client_floor` on `GET /api/discovery/home`, with a 12-month window;
  - `device_caps` clamped to a per-platform ceiling, and HLS with fMP4 segments;
  - a cloud TV code that a phone signed in to the home approves, with no cloud account needed;
  - a store-review home;
  - a cast grant with its own `cast` principal in the role tables.
- The cloud migration slot `0058` is planned. No home migrations.
- README §12 lists 12 owner questions, each with its default.

## What we learned
- The worktree branch started 300+ commits behind `origin/main` (epics only up to 43). Reset it
  to `origin/main` before writing any spec.
- **Platform facts:**
  - Roku states it does not support right-to-left text.
  - Samsung Public Seller accounts can launch only in the US; anywhere else needs a Partner Seller
    contract.
  - Samsung MSE is capped at FHD, so 4K needs AVPlay.
  - Cast receivers take no HEVC in MPEG-TS.
  - Neither Tizen nor webOS gives a web app mDNS or SSDP.
- **What the repo already has:**
  - The Android TV app has no Google Play Services dependency; only the phone Player uses the Cast
    framework.
  - HLS is TS only (`ffmpeg.go`).
  - The OpenAPI document is `shared/api/openapi.yaml`, and the cloud has none.
- **The wiki generator:**
  - Its Latin-share rule (at most 20% of the Arabic prose letters outside code) trips on
    brand-heavy excerpts. Transliterating brand names outside code spans brought every entry
    under the limit.
  - A new epic past 57 renames the chunk files `wiki-epics-55-57.json` to `wiki-epics-55-58.json`.
  - `docs/wiki/epics/epic-NN-*.md` and the `docs/wiki/INDEX.md` row are written by hand; the
    generator does not make them.

## Open
- Whether Cast receivers enforce Chrome's Local Network Access for LAN HTTPS names: a spike on
  real devices is the first task of story 58.07.
- The build PRs, starting with Phase 1: 58.01, 58.02, 58.03, 58.04, the catalog check of 58.15,
  and 58.16.
