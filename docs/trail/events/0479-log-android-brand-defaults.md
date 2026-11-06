---
schema: 1
kind: pr
pr: 479
title: "48.11: Android brand defaults"
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`ThemeMode.parse` falls back to Dark. One dark-parent `Theme.Maktaba` (`values-night` deleted). Material's `scrim` is left at its default, because the light scrim token is cream.

## What we learned
- Failed first: screenshots drew black headings until the harness had MainActivity's `Surface`.
