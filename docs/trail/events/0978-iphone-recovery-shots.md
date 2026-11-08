---
schema: 1
kind: pr
pr: 978
title: maktaba.dev shows the iPhone Remote's recovery screen
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [959]
---

## What happened
- The Remote page on maktaba.dev had no iPhone recovery screenshot since #918. It now has four, dark theme: the recovery screen in safe mode and the restore backup picker, each in English and Arabic.
- They come from CI like the other iPhone shots: the Remote UI tests write them through `MAKTABA_SHOTS` (the `remote-ui-tests` artifact of the run on the head #959 merged) against MockHome's made-up console, converted to 540 px WebP with no metadata.
