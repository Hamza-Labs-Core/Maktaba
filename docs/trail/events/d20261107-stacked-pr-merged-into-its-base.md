---
schema: 1
kind: incident
title: A stacked PR merged into its base branch, not main
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
prs: [863, 869, 879]
---

## What happened
- PR 869 (plugins on Windows, the second of two for issue 710) was stacked on the branch of PR 863 (the relay). Its merge watcher waited for PR 863 to merge, then merged PR 869 when its checks were green.
- PR 863 merged into main at 18:17:02. PR 869, whose base was still the relay's branch, merged into that branch at 18:17:39. Its merge commit is not an ancestor of main, so its change never reached main, and the issue stayed open.
- Only 3 checks had run on PR 869: CI runs its full set on pull requests into main, and a few on one into another branch. All 3 were green, so the fail-closed check (every check completed and green, at least one check, merge state clean) let it through.
- PR 879 carries the same commits into main, with main merged in and the full CI run.

## What we learned
- A merge watcher that checks only the checks it sees is satisfied by a PR into a branch: few checks run there, and the merge does not touch main.
- Changing a PR's base starts no CI run; a push does.

## Keep doing
- Merge only PRs whose base is main: the merge check refuses any other base.
- For a stacked PR, once its base PR merges: retarget it to main (`gh pr edit N --base main`), merge main into its branch and push (that starts the full CI run), then let the merge check decide.

## Stop doing
- Merging a stacked PR the moment its base PR merges, with its base still the old branch.

## Open
- None.
