---
schema: 1
kind: pr
pr: 1577
title: "No Plex in the Remote's attention fixture; maktaba.dev drops two old-library Fire TV shots"
date: 2026-11-24
session: e1c12b82-6431-40f4-b0ae-96e43fdfa26e
agent: claude-code
---

## What happened
- The Android Remote's Needs attention fixture (FakeRemoteHome.ISSUES) had a "Plex is
  unreachable" group. maktaba.dev and the store gallery stand-ins are cut from its
  references, and store listings keep third-party names out. The group became a first-party
  cause with the same "change the settings" action (permission_denied).
- The Remote's references were re-recorded with Roborazzi on the JVM (JDK 17, no daemon left
  running). 24 changed on the Mac:
  - 21 really changed: attention, approvetv and pending on the fold covers, about 0.5% each.
  - 3 were noise of 0.01-0.10% (diagnostics_error_group, diagnostics_logs, report_form)
    and were restored.
- A phone-size Arabic attention reference joins the English one. maktaba.dev's
  android-remote-attention images are cut from the pair.
- maktaba.dev's firetv-home and firetv-library were taken on the old demo library (Sintel's
  poster). Both shots and their four files were removed.

## What we learned
- Measure each re-recorded reference against its committed copy (pixels over a small delta)
  before committing. A Mac recording moves unrelated references by a fraction of a percent.
- maktaba.dev's screenshot set mixes test-data screens with demo-library ones, so it is not
  safe demo media as a set.

## Keep doing
- Fix a picture's content at its fixture and re-record, never edit the image.

## Open
- Fire TV on maktaba.dev now has only the sign-in shot. The new demo library's Fire TV
  captures can restore Home and a library.
