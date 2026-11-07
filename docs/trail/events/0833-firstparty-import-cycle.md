---
schema: 1
kind: pr
pr: 833
title: The first-party runners start again (no import cycle)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [804]
---

## What happened
- Since 0.3.93, every first-party plugin runner (`light`, `fp`, `ml`, `index`) exited 1 about 0.2 s after starting.
  - The runner's `main` imports `maktaba_pipeline.plugins.firstparty` before anything loads the stage graph.
  - `firstparty` imported `engine.graph` at its top, and the graph builds its first-party stages from `firstparty.stage_def` as it loads.
  - The supervisor marked each runner failed after five exits.
  - The worker ran every built-in plugin's stages in-process (`runner_not_serving_here`). No job was lost, but the plugin path never ran on a home, and the waves' flip to `plugin` changed nothing.
- `firstparty` now imports the graph inside `stage_def`.
- `tests/plugins/firstparty/test_firstparty.py` imports each entry point first, in a fresh interpreter: the runner's sequence, the bundle then the graph, and the graph then the bundle.

## What we learned
- The update rehearsal's wave scenario (16.1, #804) found it on its first run.
  - It runs each release's real pipeline wheel, installed as a home installs it.
  - It saw `job_stages.impl` turn `plugin` while every job still ran in-process, and the home's log held the runners' tracebacks.
- The plugin suites never saw it: their conftests import the graph first, so the cycle never formed in a test process. The parity proof drives runners through a harness that does the same.
- The affected-tests map is the coverage of main's runs, so a test file new in a pull request is in it under no source. Such a test links to its source only by name (`firstparty.py` → `test_firstparty.py`); an import added to it links nothing until main's next map.
- A worker that falls back says so only in a log line, and the job still ends done. Nothing in the queue's state shows that a stage meant to run as a plugin ran in-process.

## Keep doing
- Test a new process entry point by importing it first in a fresh interpreter (a subprocess), not only from inside the test suite.
- Rehearse a release as it is installed: the wheel in its own environment, not the source tree.

## Stop doing
- Importing a module at the top of another that the first one imports back while it loads, when either can be the first import of a process.

## Open
- The runners' crash loop showed in no health surface the scenarios read. Whether a home should report runners stuck in `failed` while their stages are `plugin` is open.
