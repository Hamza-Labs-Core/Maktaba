---
schema: 1
kind: incident
title: The 0.3.96 plugin runners started before the venv and crash-looped
date: 2026-11-08
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- The live Mac home's update to 0.3.96 started on 2026-11-08. While the server was still installing the new release's packages into the venv (`<home>/server/venv`), the supervisor started `plugin-runner/light`, `ml-1`, `ml-2` and `index`. Each died in about 15 ms with `python: No module named maktaba_plugin_runner`. After 5 exits the crash loop marked each one failed, to retry in 5 minutes. The venv was ready about a minute later. The pipeline's workers started only then, at 04:34:29Z.
- Root cause: `appBundleGates` (`cmd/maktaba-server/serve_appbundle.go`) gave the venv step (`venvGate`, a `sharedStep`) only to `RolePipeline`. Plugin runners fell into the `dbStep != nil` case, so they waited for the database step alone. That step finished before the venv did, and the runners started.
- After the venv was ready, the runners still crash-looped: `ImportError: cannot import name 'stage_def' from partially initialized module 'maktaba_pipeline.plugins.firstparty'`. The runner's `main` imports `firstparty` first. `firstparty` imports `engine.graph`, whose module-level `_fp("thumb_small")` imports `firstparty.stage_def` while `firstparty` is still initialising. Main no longer fails, fixed incidentally by Epic 54 wave 4's moves, but nothing guarded it. The pipeline suite imports `engine.graph` before `firstparty` in the same interpreter, which hides the cycle. The rehearsal's `builtIn` helper even imports the graph first, with a comment that it avoids a cycle.
- The fix: `supervisor.RunsFromVenv` names every child that runs from the pipeline's Python environment (the pipeline role and every plugin runner). `appBundleGates` gives each of them the venv step. A runner waiting on that step is never started, so it adds nothing to the crash loop.

## What we learned
- A start gate keyed on one role misses processes added later that share that role's dependency. Key it on the dependency (what the child runs from). Add a test that builds every child and checks each `python -m maktaba_*` one is gated (`TestEveryPythonChildRunsFromTheVenv`).
- Import cycles show only in a fresh interpreter, imported in the order the child imports. `pipeline/tests/test_child_imports.py` reads the modules the supervisor launches from its Go sources. In a `python -I` subprocess, it imports each child's own sequence, then each entry-point import alone. Run against v0.3.96's sources, it reproduces the runner's `ImportError`.
- A rehearsal that only checks the site serves passes a release whose runners crash-loop. The wave scenarios (16.x, each release's real pipeline) now fail when the supervisor marks any process failed after the update (`assertNoCrashLoop`).

## Open
- The other rehearsal scenarios run a fake pipeline wheel without `maktaba_plugin_runner`, so their runners may fail by design. The crash-loop check runs only in the wave scenarios. A fake runner module in the fake wheel would let every scenario run the check.
