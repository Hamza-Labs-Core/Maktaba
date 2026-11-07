---
schema: 1
kind: pr
pr: 846
title: merge-main.sh merges when main adds more entries than the branch
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [826, 795]
---

## What happened
- `tools/dev/changelog_merge.py` (#826) stopped on a CHANGELOG it should have merged, merging main into #795 after #781: "both sides changed the text between entries".
  - It counted the blank lines that separate entries as text between entries.
  - Each added entry brings one, so when main added more entries than the branch, the two sides' blank lines no longer matched.
- Blank-only parts are no longer compared. `merge-main-test.sh` case 8 has main add two entries and the branch one.

## What we learned
- #826's tests had both sides add one entry each, so their separators matched by coincidence. The case that breaks it is the common one: main gains several entries while a branch waits.
- The tool failed safe: it stopped with the merge in progress and changed nothing. #795 was resolved with the fix on a scratch copy, then checked by hand against main's file.

## Keep doing
- Test a merge tool with uneven sides (one side adding more than the other), not only symmetric ones.

## Open
- None.
