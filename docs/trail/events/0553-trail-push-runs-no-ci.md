---
schema: 1
kind: pr
pr: 553
title: A trail-only push to main starts no CI or rehearsal run
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [552]
---

## What happened
- Merge 01133fa1. A push to main or release/** that changes only docs/claude-trail.md starts no CI or Update rehearsal run (`paths-ignore` on both push triggers), so a trail merge no longer joins the `ci-main` / `update-rehearsal-main` group and cancels the previous code commit's runs, which the tag guard needs. coverage-map.yml follows CI, so it skips them too. `ci_paths.py which --push` models it; test_ci_paths.py checks both triggers.
- Background: trail PR #552 (684a653b) cancelled bdad891d4's update rehearsal. The coordinator cancelled #552's runs and re-ran bdad891d4's rehearsal (a re-run gets its own group, #547).
- The coordinator's tag job now follows main (newest commit with both runs green and not cancelled) instead of one pinned sha.

## Keep doing
- Trail-only commits are never tagged; tag the newest code commit (Release flow, deploy/packaging/README.md).
