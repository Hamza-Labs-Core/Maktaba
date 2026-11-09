---
schema: 1
kind: pr
pr: 1062
title: Moved stages open the file through the job's path map
date: 2026-11-09
session: b7095d77-a4dd-4b22-9468-dcb6f082ae2b
---

## What happened
- Thumbnail, probe and extract, moved into plugins in v0.3.109, still called the process-wide `to_local`, which the plugin runner never sets: it hands each job its path map on `ctx`. On the live home, whose `media.path_map` maps `/Volumes/Originals/` to the NFS mount, every thumbnail on the share failed as `file_missing` (item_permanent) from the install of 0.3.110 on.
- The three stages now use `ctx.local_path`.
- `pipeline/tests/plugins/firstparty/test_stage_paths.py` opens the mapped path in each stage with the process-wide map empty, and checks that no first-party plugin imports `engine.paths.to_local`; both fail on the old code.
- The failed thumbnails are queued again by the next completed scan (`engine/missing_files.py`). Merged as fd4bdfb4d on head a9f6ba666.

## What we learned
- Moving a stage out of process drops every process-wide setting it read. The parity suite ran with no path map, so in-process and plugin sides agreed on the wrong answer; only the live home's NFS map exposed it.
- An `item_permanent` failure class on a whole share is a deployment-shape bug first, not bad files.

## Keep doing
- When moving code across a process boundary, list the module-level state it reads and pin each one with a test that leaves the global empty, plus a guard against importing it.

## Open
- No sweep was made of other process-wide state (settings, environment) the moved first-party stages may read; the guard covers only `to_local`.
