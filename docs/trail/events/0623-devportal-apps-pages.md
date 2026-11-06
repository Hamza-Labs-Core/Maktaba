---
schema: 1
kind: pr
pr: 623
title: maktaba.dev Apps pages with screenshots on test data
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge 9af6cbe3c. maktaba.dev gained an Apps section: `/apps/` plus pages for Player, Remote, TV, the server and the web app with big screen, in English and Arabic, built from `web/packages/devportal/scripts/apps.mjs`.
- 47 dark WebP screenshots on test data only: the Apple CI UI-test artifacts and Mac test attachments, the Remote from `StatusUITests` on a throwaway simulator, Android screenshot-test references, and a scratch branch stack (own Postgres, HOME, `MAKTABA_CLUSTER_DIR`, `MAKTABA_ARCHIVE_DIR`) for the web and big screen.
- The leak scan checks file names and image metadata (EXIF/XMP refused, PNG text scanned), and a new parity check covers the Apps texts and images.
- Android screenshots whose fixtures showed a personal home name, a LAN address or a media path were left out, so Android TV had no image yet (fixed by #626).
