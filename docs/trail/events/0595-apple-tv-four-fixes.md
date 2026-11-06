---
schema: 1
kind: pr
pr: 595
title: Apple TV: live banner, logo on filler, Home rows, library art
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 696022a3d. The live banner stayed up after any start slower than 6 s and the mini-guide never hid; both are now timed by a `LiveOverlays` model in MaktabaKit, and a CI UI test checks the picture fills the screen and the logo corner stays clear.
- The burned-in logo was missing from filler because `tv.watermark_skip_filler` defaulted to true; it now defaults to false, and `write_channels` v5 (migration 0173 `write_channels_logo_on_filler`, SchemaRev 173) re-stages every channel.
- The TV home called only `/api/recommendations`; it now builds the Player's rows from a shared `HomeShelves` model with retry tiles. Library tiles drew an icon; they now show each library's newest small poster.
- Added the first CI job for tvOS UI tests and a cross-service test that stage version bumps carry a migration and SchemaRev.
- Two iPad Player fixes found along the way: `PlayerTracks.sound` read only the first enabled audio track (reported `sound=audio` after a switch, failing on main too), and the permissions walkthrough could stay up after Continue; Continue now calls the sheet's own dismiss.
