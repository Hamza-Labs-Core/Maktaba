---
schema: 1
kind: incident
title: "The live home installed v0.3.157 below its rolled-back v0.3.158 from a catalog frozen by GitHub's 8 MiB release list, and rolled back again"
date: 2026-11-24
session: 6356cdc7-6a9a-4376-9fb9-a1677d6fe688
agent: claude-code
prs: [1592]
---

## What happened
- **2026-11-24 08:00Z:** the live home (v0.3.153) chose **v0.3.157** in its window (run
  f18be9ee), not v0.3.161 (published 02:41Z, the first release with #1538).
  - v0.3.157 failed on 0235's `CREATE INDEX CONCURRENTLY` lock timeout like v0.3.158 did on
    2026-11-23.
  - From 08:20Z it put the 34 GB dump back; the last restore took 2 h 44 min.
- **Cause: the home's release catalog had been frozen since 2026-11-23 05:02Z.**
  - `Discoverer.list` reads `GET /releases?per_page=30` with an 8 MiB cap.
  - With some 190 assets a release, that answer is 10.5 MB (measured on 2026-11-24).
  - From 2026-11-23 11:02Z, every 6-hour check logged
    `update_check_error … larger than 8388608 bytes` (the api's log segments).
  - The last good check ran before v0.3.159 was published (05:12Z on 11-23), so the catalog
    ended at v0.3.158, blocked `rolled_back`.
  - `ChooseTarget` (v0.3.153's `target.go`) chose the newest release it knew that wasn't
    blocked: v0.3.157, below the rolled-back one. v0.3.156 (its own 0235) and v0.3.155 would
    have come next, one window each.
- **The same cap is in v0.3.161 and on main**, so no home on any version could discover any
  release. The About page's check (`system/updater.go`) and the server's self-update
  (`selfupdate`) read the same list with the same cap.
- **#1592:**
  - The catalog reads the list in pages of 10.
  - A release that rolled back (or whose migrations failed on the rehearsal's copy) holds back
    every release below it, and every newer one with the same migration set, until a newer one
    changes it. **Update now** naming a release still installs it.
  - `release.json` gains `migrations.set_sha256`, a digest of the migrations' names and texts.
    The archive's own digest changes every release, because its entries carry the commit time.

## What we learned
- A failed check didn't stop installs: the scheduler kept choosing from a stale catalog, and the
  only alarm (`StaleAfter`) waits 14 days.
- Byte caps sized to a response that grows with every release (30 releases × assets) fail
  silently for everyone at once, on old and new releases alike.
- The open item in d20261123-cic-lock-timeout ("ChooseTarget can still pick a release older
  than one blocked for rolled_back … harmless here, since v0.3.159 exists") was not harmless:
  the home didn't know v0.3.159 existed.

## Keep doing
- Read the live home's own log segments (`/Volumes/Big/Maktaba/server/logs/segments/api`)
  and the journal before reasoning from code: the `update_check_error` lines gave the cause
  within minutes.

## Stop doing
- Assuming the home's catalog knows a release because GitHub has it.

## Open
- The live home still runs v0.3.153, whose updater can't see v0.3.161 or any later release.
  - Its next window (2026-11-25 08:00Z) would choose v0.3.156, then v0.3.155.
  - It needs an action by the owner: turn automatic installs off, defer them, or skip
    0.3.154–0.3.156 on the Updates page, or mark v0.3.154–v0.3.160 as prerelease so the
    pre-run recheck withdraws them.
  - Then a manual install of a release with #1592.
- Every home in the field on a release before #1592 is in the same state and needs a manual
  install to see releases again, unless the newest 30 releases' list falls below 8 MiB.
- An automatic run doesn't wait for a fresh catalog: a check that has failed for hours should
  probably hold automatic installs.
- The Android and Apple apps show `held_back` through their fallback text only.
