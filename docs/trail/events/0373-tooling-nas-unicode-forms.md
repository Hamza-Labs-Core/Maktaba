---
schema: 1
kind: pr
pr: 373
title: NAS file names may be NFC or NFD
date: 2026-10-27
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Streaming failed on NAS files whose names were stored in the other Unicode form. Paths may be NFC or
NFD; #373 plays both.
