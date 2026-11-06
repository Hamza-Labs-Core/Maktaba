---
schema: 1
kind: decision
pr: 288
title: Updating live means a tagged, signed CI build
date: 2026-10-25
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [304]
---

## What happened
Updating the live install is: merge, tag a release, CI builds a Developer ID signed and
notarized dmg (#288, notarized with an App Store Connect API key), and the app updates itself.
Intel Mac desktop builds were dropped; Macs are Apple silicon only (#304).

## What we learned
- Local builds carry a placeholder updater key, so a local build cannot stand in for a CI build on the live install.
