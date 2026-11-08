# Story 58.18 — Amazon Vega OS: a React Native app, built when Vega devices are a real share

> Epic 58 · Phase 6 · Depends on: 58.01, 58.02, 58.03, 58.04, 58.05 · Needed by: —

## Why

Amazon launched Vega OS, a Linux-based Fire TV operating system, on the Fire TV Stick 4K Select
in 2025 (research.md §2.2). Since then Amazon has added:
- the Fire TV Stick HD (2026);
- the Fire TV Stick 4K 3rd gen (2026-09-30).

Reports say every future Fire TV Stick will run Vega. Android apps do not run on Vega, and there
is no sideloading, so the Fire OS build (58.05) does not reach these sticks. A Vega app is
**React Native for Vega** (or a web app in Vega WebView), with a W3C media API on GStreamer:
HLS and DASH through MSE (Shaka, hls.js or dash.js), and MP4 or MKV played directly.

## Description

**When.** This story starts when either:
- Vega devices reach 25% of Fire TV sign-ins to Maktaba homes, counted from the
  `X-Maktaba-Client` platform (`vega` against `firetv`) in the cloud's anonymous relay analytics (Epic 30;
  the platform is added as one more aggregate count there if it is not one already), and from
  Amazon's published device figures;
- or Amazon stops selling Fire OS sticks in a market Maktaba lists.

Until then, a Vega stick owner is told on maktaba.tv's Apps page that the app is coming, and that
Cast or AirPlay from the phone works meanwhile. A Vega stick is not a Cast receiver, so Cast is
not offered for it. That is checked again when the story starts.

**What it is.**
- `apps/tv/vega`: React Native for Vega (the `@amazon-devices` packages), in TypeScript.
- It shares with the web TV code:
  - `apps/tv/web`'s platform-free modules (the home probe, the cloud code client, the compat
    check, the API client);
  - the `/tv/` screens' state logic, by moving it into `web/packages/tvcore` (new) when this
    story starts.

  React Native for Vega uses its own components, so the screens are rebuilt in RN with the same
  layout, focus rules and strings.
- **Player.** Vega's W3C media API with Amazon's patched Shaka for HLS (TS and fMP4), direct play
  for MP4 and MKV, and `device_caps` from its capability queries (58.02, profile `vega`).
- **Home reach.** The cloud code (58.03), the subnet probe, and a typed address, as 58.09.
- **Strings.** The launcher's and `/tv/`'s catalogs. RN's `I18nManager` handles RTL, and the
  catalog check covers it (58.15).

**Store.** The same Amazon Developer Console as 58.05, with a VPKG build and Live App Testing.

**CI.**
- Jest for logic.
- The Vega Virtual Device runs on Ubuntu with KVM, and Amazon documents headless SDK installs for
  CI but skips the device in its examples (research.md §2.2). So the device run is a
  non-required job where the runner has KVM.
- A real Fire TV Stick 4K Select is required before each submission.

## Acceptance criteria

- **Given** the start condition is met, **then** a Fire TV Stick 4K Select signs in by cloud code
  and plays a 4K HEVC HDR10 MKV in direct play.
- **Given** Arabic, **then** the app mirrors and every key has its Arabic (catalog check).
- **Given** the start condition is not met, **then** maktaba.tv's Apps page names Vega sticks and
  the way to watch meanwhile, in English and Arabic.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Jest | shared modules under RN | the 58.01 and 58.03 vectors | pass |
| T02 | Vega Virtual Device (non-required, KVM runners) | app with a mock home | sign in, browse | screenshots en and ar, dark; device log in the report |
| T03 | Real-device checklist | Fire TV Stick 4K Select, Fire TV Stick 4K 3rd gen | each submission | formats, HDR, Arabic, Back |

## Strings

The shared catalogs; no Vega-only strings except:

| Key | English | Arabic |
|---|---|---|
| `compat.updateApp.vega` | Open Apps on the Fire TV, find Maktaba and update it. | افتح «التطبيقات» على Fire TV، وابحث عن Maktaba وحدِّثه. |

## Edge cases

- **Amazon's Cloud App Program**, which streams an Android app to a Vega device, is for chosen
  partners only. It is not a path for Maktaba.

## Build plan

| File | Change |
|---|---|
| `apps/tv/vega/` (new) | the app |
| `web/packages/tvcore/` (new, at story start) | shared state logic |
| `.github/workflows/_vega.yml` (new) | T01, T02 |
| `docs/stores/amazon.md` | the Vega section |
