---
schema: 1
kind: pr
pr: 1444
title: "SECURITY.md and the advisories that affect this version in Settings → About (story 23.8)"
date: 2026-11-19
session: 3592ba55-275b-49f6-842b-123bf5ccaec9
agent: claude-code
---

## What happened
- #1444 merged as a28ba192e. Story 23.08: `SECURITY.md` (private reporting, 3 business days
  to acknowledge, 90 days to a fix or a coordinated disclosure), and the home's update check
  reads the repository's published security advisories (`api/internal/system/advisories.go`):
  `GET /api/system/updates` carries those affecting the running version, Settings → About lists
  them.
- Two review rounds (Kimi K3.1 Pro) found the list read only its first page, the "update to
  fix them all" release could still be affected on another branch, the page's own version
  comparator ordered prereleases wrongly, the API level-1 snapshot lacked the fields, and a
  joined machine showed the home's list against its own version. All fixed: the Link header
  is followed (20 pages at most, then unknown), the server computes `advisories_fixed_in` (the
  lowest patched release in no affected range), and a version mismatch reads as unknown.
- Rebased twice on main for `docs/wiki/epic-status.md`; its Total row was stale on main and
  is now recomputed from the epic rows.

## What we learned
- An advisory's patched versions name several branches: the release that fixes all of a set
  is not the highest per-advisory fix, it is the lowest patched release no range covers.
- `/api/system/updates` is home-served while the version a page shows is the machine's own:
  anything matched to a version must check they are the same.

## Keep doing
- Recompute the status page's summary sentence and Total row from the epic rows after each
  conflict, not by hand.

## Open
- TC1 (the tabletop drill) and TC3 (a synthetic-CVE patch release) are process exercises,
  not done in code.
