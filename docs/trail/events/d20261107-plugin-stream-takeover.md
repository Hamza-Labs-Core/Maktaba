---
schema: 1
kind: incident
title: Taking over the plugin stream after the weekly limit
date: 2026-11-07
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [848, 879, 831]
---

## What happened
- The previous delegate stopped at the weekly limit with four background processes still alive: merge watchers for #848 and #879, the #851 retarget script and the #799 slot waiter. Their `gh pr merge` had no `--match-head-commit`, so a push between the check and the merge could have merged an unchecked head. All four were stopped by PID and replaced by one watcher per PR. Each watcher pins the head sha and stops when the head moves; an optional list of checks must have run and passed, not been skipped.
- #879 merged first (94 checks). #848 then conflicted in `plugind/crates/core/tests/host.rs`, as the handoff expected. Both appended test blocks were kept, but the closing brace after the conflict markers belonged to both functions, so one brace had to be added for the first.
- #831 (a rollback runs the release's Down scripts) had no owner after the handoff. The last commit came from the session that handled the 0.3.90 rollback, at 18:40 UTC. Its unit tests covered the logic, but no update-rehearsal scenario drove the new path: 03 keeps the schema, and 04 to 06 put the dump back. Scenario 03b was added: the failing release adds a CHECK, nothing is lost, and the schema-keep rule cannot keep it. The rollback must undo it with the Down scripts, with no dump and no restore.

- **#848 merged at 19:01 UTC with three checks still running** (`Analyze (rust)`, `plugins / plugind`, `plugins / plugins-sandbox (windows-2022)`). Its watcher was running `watch-merge.sh` when that file was edited in place to add the required-checks list. Bash reads a script as it runs, so the running watcher resumed at a shifted byte offset inside the new text and reached the merge line, even though its check had just failed. `--match-head-commit` did not help, because the head was the right one. `Analyze (rust)` and `plugind` then finished green; the Windows sandbox check was followed to its end.

## What we learned
- Never edit a script that a background process is running. Copy it under a new name, and start new watchers from the copy.
- A new rollback path is not proven by its unit tests. The update rehearsal must have a scenario that takes it, and the merge must wait for that scenario by name, because a check that was skipped by a path filter counts as passing.
- When a conflict hunk ends just before a shared closing line, keeping "both sides" leaves the first block unclosed. Compile after resolving, every time.

## Keep doing
- Read the old delegate's watcher scripts before adopting them; replace them rather than run two generations side by side.
