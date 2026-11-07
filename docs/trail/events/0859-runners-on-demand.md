---
schema: 1
kind: pr
pr: 859
title: Maktaba's built-in plugin runners start only when they have work (the Core budget)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [833, 844]
---

## What happened
- After #833 made the runners start, an idle Core install ran its shared runner groups as idle Python processes. Main went red on `mac-app / Bundle size (Core payload)`: 6 processes and 731 MB against 4 and 700. #844 (waves 5–8) showed 9 and 909 MB.
- The shared groups now start on demand. The worker runs a job itself while its runner is down and asks the supervisor for it (`POST /runners/{group}/start`). The runner exits 0 after ten idle minutes, and the supervisor doesn't restart it (`Process.IdleOnClean`) until it's asked again.
- Each group is held while none of the components its plugins serve is on (read from the bundle's manifests). The start endpoint refuses a held group with 409 and never opens a gate.
- The supervisor reports each process's start mode (`always`, `on_demand`). Update rehearsal scenario 12 reads it: a new on-demand process must be held (gate closed) or idle (gate open). A kept one that the previous release reported without a start mode may come back on demand, held or idle, logged as an expected change; every other row stays exact. `healthyFor` and adoption never wait on an on-demand process nobody asked for.

- CI on this pull request first went green without the check it was for: `mac-app` (with `Bundle size (Core payload)`) was skipped, because `.github/ci-paths.yml`'s `mac_app` filter named none of the files that decide what runs on an idle Core home. The filter now names them (the supervisor's process, component, gate and runner files, `shared/components`, `shared/plugins`, the plugin runner, the worker's ask for one), with tests in `tools/ci/tests/test_ci_paths.py`, and the check ran on this pull request before it merged.

## What we learned
- A process that crashed at start hid a budget breach: the fix for the crash (#833) is what exposed it. When fixing a start-up crash, check the steady-state budgets it was hiding.
- "Runs while its component is on" is the wrong lifetime for a runner whose components are on in every home. Tie it to work instead. The worker's in-process fallback already covers the moment before the runner is up.
- The supervisor's health check must not count as activity, or an idle timeout never fires.
- A green run proves only the jobs that ran. Before merging a fix, confirm that the check it fixes actually ran on the pull request; a path filter can skip it silently. A job that measures a whole-product budget needs every input of that budget in its filter.
- When a process changes how it starts, model the new state in what the rehearsal reads (the start mode) instead of special-casing a version: "the bundle has it" then has a true reading for an on-demand process.

## Open
- None.
