---
schema: 1
kind: pr
pr: 583
title: Mac Player and iPad Player join the TestFlight upload
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge a2b97e892. The Mac Player and the iPad Player join the TestFlight upload on every tag.
- An unsigned archive carries no entitlements, so the Mac leg signs `Player-Mac.entitlements` into the archived app ad hoc before the export, keeping its sandbox.
- The Mac app had no icon until then (the single-size macOS icon entry was ignored); it now has every Mac size, plus `ITSAppUsesNonExemptEncryption: NO`. `test_testflight.py` covers it.
- Records: TV (6830067622) and Mac created through Xcode's Organizer on 2026-11-03 (POST 201; Xcode's error after it is harmless).

## What we learned
- An unsigned archive has no entitlements; sign them in ad hoc before export or the sandbox is lost.
- A single-size macOS icon entry is ignored; supply every Mac size.

## Open
- The iPad record's creation got HTTP 500 from Apple and was still missing.
