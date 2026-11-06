---
schema: 1
kind: pr
pr: 599
title: docs/hardware.md measured on the live home
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge d78f92b2e. `docs/hardware.md` says what home and worker machines need, measured read-only on the live M3 Ultra home (v0.3.62).
- It transcribes 1,125 audio-hours a day (about 47x real time, 4 files at once, second listen on every transcript). The GPU is 89-98 % busy while the CPU is 72 % idle, so GPU compute is the bottleneck.
- The 14,206 audio-hours still queued take about 13 days; the earlier estimate of a day or two for about 45k videos was wrong by about 10x. The database is 64 GB (44 GB search vectors), about 2.8 GB per 1,000 transcribed audio-hours.
- Every speed is marked measured, from the code (2080 SUPER 72x Whisper alone, 43x with second listen; CPU 2.6x on 10 threads), or estimated with its method. The README has a short summary linking to the page.


## Open
- Live TV encoder CPU not measured.
