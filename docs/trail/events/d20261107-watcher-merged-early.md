---
schema: 1
kind: incident
title: A merge-on-green watcher merged four PRs with their checks still running
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- A bash watcher meant to merge #792, #793, #794 and #797 when green used `grep -P` to find failed and pending checks. macOS's grep (BSD) has no `-P`: each check failed with a usage error, matched nothing, and the script took "no failure, no pending" as green and merged all four within its first pass.
- The checks went on running on each PR's head after the merge. All four finished green, so main was never broken, but no merge waited for its CI.

## What we learned
- A test that passed in zsh says nothing about bash: the session's zsh had a GNU grep on its path, while the bash script ran `/usr/bin/grep`.
- A watcher must count "could not read" as "not green". An error in a check must stop a merge, not allow it.

## Keep doing
- Watchers only report. Merge by hand, after reading `gh pr checks` yourself.
- Match a tab with `grep -E "$(printf '\t')pending"` (portable), never `grep -P`.

## Open
- Nothing.
