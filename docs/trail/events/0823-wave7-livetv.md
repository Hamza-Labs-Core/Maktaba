---
schema: 1
kind: pr
pr: 823
title: Live TV's channel planning and TV changes run as Maktaba's built-in plugins (Epic 54 wave 7, 54.34)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [806]
---

## What happened
- `plan_channels` and `write_channels` moved into `tv.maktaba.livetv`, and `tv_stage`, `tv_swap`, `tv_rollback` and `tv_gc` into `tv.maktaba.livetv-slots`, in the `light` runner. Roles: migration 0207.
- The swap commit and the rollback write `app_settings` (the release's TV settings). The worker runs them for the job's own change (relayed `tv_commit` and `tv_rollback`).
- The worker sends the names of its plan exporters to the runner, which resolves them from the extensions it discovers.
- The TV release code's statements are fixed texts, and the `channel_art_overrides` existence probes are gone.

## What we learned
- Two kinds of core code get imported from a stage module: code the core itself needs (the plan preview's inputs, the link target), and code only the stage uses. Move the first kind into a core module before moving the stage, or the core ends up importing plugin code.
- A per-process registry (extensions' plan exporters) doesn't exist in a runner. Send the names with the job and resolve them there, and fall back to the worker when a name doesn't resolve.
- TV schedules follow the clock. Freeze `now` in each module that imported it by name (`monkeypatch.setattr(mod, "now", ...)`), and leave the engine's own clock real.
- Rules sections read deep in shared code (`dizquetv` in `channels.repo`, `display_titles`) only show up when the proof runs the path that reads them. A `plugin_scope` refusal with an empty message is the settings check.
- Rebuild the bundle (`tools/plugins/bundle.py --write`) after every `statements_build.py sync`. The plugin API serves the bundle, not the `queries/` folder.

## Open
- The update-rehearsal scenario 16.7 (T02): issue #804.
- N16 for `tv_stage`, `tv_swap` and `tv_rollback` isn't measured, because the bench can't rerun a state transition. Their rows are compared after each step.
