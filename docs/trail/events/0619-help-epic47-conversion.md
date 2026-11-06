---
schema: 1
kind: pr
pr: 619
title: In-app help: Epic 47 stages shipped, conversion explained, YouTube sources on Manage libraries
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge cc51746c8. The in-app help still said the five Epic 47 stages arrive in a later version after migration 0170 had turned them on; that text was removed in English and Arabic.
- It documents how a conversion decides (skip, remux, AAC 192k audio, or an H.264 re-encode that needs `convert.allow_reencode`), and that migration 0166 left every existing channel as a source set to Off.
- There is no per-video Convert and nothing reads `convert.auto` yet; the help says so and points to the item page's Choose queues... and the library menu.
- Manage libraries has a YouTube sources... button that reuses the Queues page's dialog.
- The help coverage test reads the pipeline's stage registry and unreleased flag, so a shipped stage's entry cannot keep "not set up yet" text.
