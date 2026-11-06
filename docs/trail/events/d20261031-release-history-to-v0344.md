---
schema: 1
kind: release
title: Release history up to v0.3.44
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [2, 41, 119, 138, 435, 438, 439, 441]
---

## What happened
- Project start 2026-05-03: skeleton and architecture spec; #2 (merged 2026-05-07) holds 25 epics and 272 stories.
- Gap closure 2026-05-30: one umbrella PR, waves 0 to 3 (#41).
- v0.1.0, 2026-10-19: first release (#119).
- v0.2.0, 2026-10-19: tag only, no GitHub release; changelog in #138.
- v0.3.0-rc.1 to rc.7, 2026-10-22 to 2026-10-24: the Mac app era starts (corrected from git: the old trail said rc.1 to rc.4; rc.5 to rc.7 also exist, rc.7 published after v0.3.0).
- v0.3.0, 2026-10-24: first non-rc 0.3 release.
- v0.3.8, 2026-10-25: first Developer ID signed and notarized build.
- v0.3.20 to v0.3.26, 2026-10-26: pgvector search, licensing.
- v0.3.27 to v0.3.38, 2026-10-26 to 2026-10-28: Epics 42, 44, 45, 46; the 2026-10-27 outage at v0.3.36.
- v0.3.39 to v0.3.42, 2026-10-28: four in one day and the 2026-10-28 outage. This led to the CI-first rule.
- v0.3.43, 2026-10-31: first release after the update rehearsal (#435). It also carried #438 (a Mac stops the postgres a killed server left) and #429 (TV never blocks updates).
- v0.3.44, 2026-10-31: TV slots' ports and the kept-schema install fix (#439), and the rehearsal seed fix (#441). Live on the Mac that evening (corrected from git: the old trail credited #438 and the TV-never-blocks change to v0.3.44; both are in v0.3.43).

## What we learned
- Other sessions tag too: check `git ls-remote --tags` before picking a version number. The full list is `gh release list`.
