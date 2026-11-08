# Epic 58 — Apps for the TVs Maktaba does not reach yet: Fire TV, Google Cast, Samsung, LG and Roku

> **Status:** spec (2026-11-08). **Source:** [`specs/epics/58-smart-tv-platforms/`](../../../specs/epics/58-smart-tv-platforms/README.md).
> **Anchors:** [Epic 14](epic-14-tv-apps.md) (the TV apps; revisits its DLNA answer for Tizen and webOS) · [Epic 46](../../../specs/epics/46-big-screen/README.md) (the web's 10-foot view) · [Epic 50](epic-50-cloud-no-downtime.md) (the relay, per-home certificates) · [Epic 56](epic-56-tv-library-view.md) (the TV layout) · [Epic 57](epic-57-parental-controls.md) (the parental filter) · [research](../../../specs/epics/58-smart-tv-platforms/research.md).

## Goal

A household can watch its Maktaba home on the TV it already owns: an Amazon Fire TV, any TV or
stick with Google Cast, a Samsung, an LG or a Roku, with the Apple TV and Android TV apps' layout,
in English and Arabic, signed in by a code approved on a phone, on the home network or from
anywhere through Maktaba Cloud.

## Key decisions

| | |
|---|---|
| Order | Fire TV, Cast, Samsung and LG together, Roku, then Vega OS when its share warrants it |
| Fire TV | a flavor of the Android TV app (Fire OS is Android; the app uses no Google Play Services) |
| Cast | a Maktaba custom receiver at `cast.maktaba.tv`, senders in the Android and Apple Players and Chrome, a cast grant scoped to the items cast |
| Samsung and LG | one web-TV codebase: a packaged launcher hosting the home's own `/tv/` UI (the web's big-screen view, built for Chromium 68) through a versioned bridge; AVPlay on Samsung, native HLS on LG |
| Roku | native BrightScript/SceneGraph; Arabic shaped by the app itself, since Roku has no RTL |
| Reaching a home | a cloud code approved from a phone signed in to the home; a subnet probe where there is no mDNS; per-home HTTPS names on the LAN |
| Playback | each TV reports what it decodes, clamped to its platform's ceiling; HLS with fMP4 segments for HEVC and AV1; DV profile 8 sent as its HDR10 base where a TV lacks DV |
| Compatibility | an API level on the home; homes serve 12 months of levels; Update the home / Update this app screens |
| Rules | same API and roles, enforced on the server with role tests; Epic 57's filter applies by itself; nothing sold on any TV; no Stripe |
| Testing | emulators and simulators in CI where they exist; written real-device checklists where they don't; app logs in every report |
| Out | Xbox, VIDAA and Vizio's own platforms (reached by Cast and AirPlay), TVs before 2020, DRM |

## Stories

| # | Story | Phase |
|---|---|---|
| 58.01 | [The compatibility contract: API levels, oldest supported apps and the update prompt](../../../specs/epics/58-smart-tv-platforms/story-58-01-compatibility-contract.md) | 1 |
| 58.02 | [Playback that fits each TV: device-reported capabilities, new profiles and HLS in fMP4](../../../specs/epics/58-smart-tv-platforms/story-58-02-device-playback-caps.md) | 1 |
| 58.03 | [Sign a TV in from anywhere: a code through Maktaba Cloud](../../../specs/epics/58-smart-tv-platforms/story-58-03-cloud-tv-code.md) | 1 |
| 58.04 | [A home for store reviewers](../../../specs/epics/58-smart-tv-platforms/story-58-04-store-review-home.md) | 1 |
| 58.05 | [Fire TV on Fire OS: an Amazon build of the Android TV app](../../../specs/epics/58-smart-tv-platforms/story-58-05-fire-tv.md) | 2 |
| 58.06 | [Google Cast senders in the Android Player, the Apple Player and the web](../../../specs/epics/58-smart-tv-platforms/story-58-06-cast-senders.md) | 2 |
| 58.07 | [Maktaba's Cast receiver at cast.maktaba.tv](../../../specs/epics/58-smart-tv-platforms/story-58-07-cast-receiver.md) | 2 |
| 58.08 | [The TV build of the big-screen view, served by the home at /tv/](../../../specs/epics/58-smart-tv-platforms/story-58-08-tv-web-ui.md) | 3 |
| 58.09 | [The web-TV launcher: finding the home, signing in, and the bridge to the TV](../../../specs/epics/58-smart-tv-platforms/story-58-09-tv-web-launcher.md) | 3 |
| 58.10 | [Samsung Tizen: AVPlay, the package and the Samsung store](../../../specs/epics/58-smart-tv-platforms/story-58-10-tizen.md) | 3 |
| 58.11 | [LG webOS: the native player, the package and the LG Content Store](../../../specs/epics/58-smart-tv-platforms/story-58-11-webos.md) | 3 |
| 58.12 | [Roku: a SceneGraph channel with the TV layout](../../../specs/epics/58-smart-tv-platforms/story-58-12-roku-channel.md) | 4 |
| 58.13 | [Roku: the player, certification and release](../../../specs/epics/58-smart-tv-platforms/story-58-13-roku-player-certification.md) | 4 |
| 58.14 | [Parental controls on the new TVs](../../../specs/epics/58-smart-tv-platforms/story-58-14-parental-controls.md) | 5 |
| 58.15 | [English and Arabic on every TV: catalogs, mirroring, fonts and Roku's visual text](../../../specs/epics/58-smart-tv-platforms/story-58-15-strings-arabic-rtl.md) | 1, 4 |
| 58.16 | [CI for each TV: emulators where they exist, real-device checklists where they don't, logs kept](../../../specs/epics/58-smart-tv-platforms/story-58-16-ci-per-platform.md) | 1 |
| 58.17 | [OpenAPI, role tests and the new-clients rehearsal](../../../specs/epics/58-smart-tv-platforms/story-58-17-openapi-roles-rehearsal.md) | 5 |
| 58.18 | [Amazon Vega OS: a React Native app, built when Vega devices are a real share](../../../specs/epics/58-smart-tv-platforms/story-58-18-vega-os.md) | 6 |
