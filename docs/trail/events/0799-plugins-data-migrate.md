---
schema: 1
kind: pr
pr: 799
title: A plugin's data migrates before its new version runs (Epic 54, 54.07 §5)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- `plugin_installs.data_version` (migration 0203) holds the data's version while it is behind the manifest's; a plugin's other stages pause and enqueue its one migrate job (`p_<hash>__migrate`, catalog, home only), which calls the guest's `run("__migrate")` with `{from, to}` when the manifest says `migrate = true` and clears the column on success. Rollback compares with the data's own version. Closes #712.

## What we learned
- A plugin component has one export, `run(stage)`, and the host passes `job.stage` straight to it: the spec's "optional `migrate(from-version)` export" became a reserved stage name, `__migrate`, plus a manifest flag that says the plugin handles it (a plugin that does not would fail the job forever).
- The data's version had nowhere to live: the manifest says what a version expects, not what the data is. Rollback's `plugin_data_newer` check compared manifests, which refused a rollback before any migration had changed the data; it now compares the data's own version.
- The migrate stage is placed on the home: it writes `plugin_installs`, which `plugin_tables_guard` keeps joined machines from writing; a machine still pauses its jobs and enqueues the migrate job, which only the home claims.

## Keep doing
- Check the dispatcher's order: a pause decided before the token is minted needs no `end` call and no breaker record.
