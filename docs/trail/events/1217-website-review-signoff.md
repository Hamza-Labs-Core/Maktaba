---
schema: 1
kind: pr
pr: 1217
title: "maktaba.tv: the lead signs the Phase 1 and Phase 2 design reviews"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1217 merged as 6b9b8d4fd: `design/review-phase-1.md` signed at 7ebffe1e4 (part A's shell on
  every page; the front door's rows superseded by the home page) and `review-phase-2.md` signed
  at #1215's head for the home and download pages, with the rows #1209 and #1215 fixed updated.
  Each sign-off says how the review was made: a sample of references looked at by the lead
  beside its mockup, every difference row read, every reference compared by the pull requests
  that built the pages.
- Phase 1 is complete on main but for going live, which is the owner's decision
  (`vars.WEBSITE_LIVE`); the shared Cloudflare token has what the deploy needs.

## What we learned
- A review written while pages are still moving goes stale row by row; sign it after the fixes
  it depends on land, and update the rows they fixed.

## Keep doing
- Say in a sign-off what was looked at and what was relied on.

## Stop doing
- Leaving a review marked draft once its phase's work is merged.

## Open
- Before Phase 2 is done: the owner's read of the Arabic copy (Q2, A5); the download page on part
  B's shell; the captures held back by #1198 and #1199.
