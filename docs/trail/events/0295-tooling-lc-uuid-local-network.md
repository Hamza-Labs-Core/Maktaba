---
schema: 1
kind: pr
pr: 295
title: Local Network grants need LC_UUID in the binaries
date: 2026-10-25
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
macOS Local Network grants did not apply to Maktaba's binaries because the builds lacked an LC_UUID.
#295 keeps the LC_UUID in macOS builds.

## What we learned
- Local Network permission needs LC_UUID in the binary, and like Full Disk Access it applies only to processes started after the grant.
