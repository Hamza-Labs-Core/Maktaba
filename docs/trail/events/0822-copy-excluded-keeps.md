---
schema: 1
kind: pr
pr: 822
title: "migration-lint: cluster_copy_excluded() keeps every table"
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [781]
---

## What happened
- A new migration-lint rule, `copy-excluded-keeps`, checks redefinitions in slot order. A redefinition of `cluster_copy_excluded()` must keep every table the previous one listed, unless it names the table on a `-- cluster_copy_excluded drops:` line. Its Down must restore the previous list exactly. The repo's 18 definitions pass, and so does #781's 0204.

## What we learned
- A function redefined in full by many migrations is a merge hazard that git can't see. Two pull requests written in parallel each copy the list they saw, and both merge cleanly. Lint the content, not the slot.
- A lint that parses SQL can pass vacuously when its parser finds nothing. The history test asserts that it found the definitions and parsed the tables.
- When you renumber or merge main into a branch with such a migration, rebuild the list from the newest definition on main at merge time, and its Down from that same definition.

## Open
- Other whole-list functions may share the hazard (for example `maktaba_min_node_schema()`, which is already checked against the MANIFEST). Add a rule when a second such list appears.
