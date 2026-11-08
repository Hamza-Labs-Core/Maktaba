---
schema: 1
kind: incident
title: Two merge watchers for PR 859 could never merge it: they matched the check's name without its workflow prefix
date: 2026-11-07
session: 848393e1-ea22-4a25-a90f-b930df232381
prs: [859]
---

## What happened
- The watchers for #859 required `Bundle size (Core payload)` to be SUCCESS, matching `.name == "Bundle size (Core payload)"`. In `statusCheckRollup` the check is named `mac-app / Bundle size (Core payload)`, so the match always came back MISSING and no merge happened. Both were fail-closed, so nothing merged wrongly.
- A new watcher matched the full name exactly. Bundle size ran green on CI (4 processes, 602.9 MB idle against 700 MB), and #859 merged as 5457879a4.
- The first green run still did not merge: main had moved and the PR was DIRTY. A main merge (only the CHANGELOGs conflicted) and a second CI run were needed.

## What we learned
- Print the rollup's names (`jq -r '.statusCheckRollup[].name'`) before writing a watcher that requires a check. Reusable workflows prefix their jobs.
- A required-check watcher should also say whether the check exists at all: MISSING, not just "not green".

## Keep doing
- Fail-closed watchers with an explicit required check, bounded, PID recorded and confirmed gone.

## Open
- Nothing.
