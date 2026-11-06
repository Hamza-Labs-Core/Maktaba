---
schema: 1
kind: pr
pr: 545
title: Release tags upload to Google Play internal tracks
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 8191f3e4. Every `v*` tag uploads each signed AAB (Player, Remote, TV) to its Play internal track (status completed, mapping.txt, whatsnew en-US and ar from the annotated tag message; a `--- ar ---` line starts the Arabic) when the secret `PLAY_SERVICE_ACCOUNT_JSON` is set; else one notice and success. `PLAY_UPLOAD`/`PLAY_RELEASE_STATUS` vars removed. Packages are read from the Gradle applicationId (`tools/ci/play_release.py`). Action r0adkll/upload-google-play v1.1.5 @ e738b9dd.
- versionCode = (MAJOR*1000000+MINOR*1000+PATCH)*100+STEP (stable 99, alpha.N = N, beta.N = 32+N, rc.N = 64+N). A pre-release no longer shares its release's code; all codes are above the old ones (v0.3.54 = 3054); MAJOR capped at 20.

## Open
- Waiting on the owner at the time: a Google Cloud service account, Play Console "Release to testing tracks" on the 3 apps, and the secret (docs/google-play.md). Superseded by the keyless upload in #557.
- The Capacitor `mobile-release.yml` versionCode still collided for pre-releases (fixed in #549).
