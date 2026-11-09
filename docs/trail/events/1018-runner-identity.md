---
schema: 1
kind: pr
pr: 1018
title: A Windows runner is the process its launcher created, or it does not serve (story 54.23)
date: 2026-11-08
session: 6e034a77-9c8e-480c-9432-d2d753e4f219
prs: [986, 990, 1019]
---

## What happened
- #986 started a venv's runner as the venv's base interpreter (`venvBase`, `__PYVENV_LAUNCHER__`). If the base interpreter could not be named, it still started the venv's launcher program, whose child the file broker turns away.
- #1018 keeps that design (start the base interpreter directly) and makes it fail closed:
  - The launcher resolves `pyvenv.cfg` before any grant, beside the program or one folder up, as CPython does.
  - If the file is unreadable, names no base interpreter, or names one that is relative, not a file, the venv's own python or another venv's python, the start is refused (exit 70, step `venv`). A program with no `pyvenv.cfg` starts as named.
  - Inherited venv hints are dropped.
  - The runner checks `sys.prefix` against `MAKTABA_RUNNER_VENV`, and asks the broker a new `hello` that is answered only past its client pid check. A failed check logs `plugin_runner_venv_refused` or `plugin_runner_broker_refused`, and the jobs go to the worker.
  - The Job Object no longer keeps a slot for a launcher's child.
- Merged as 7fe991562 from head a7ab175. The Windows e2e checks each runner's identity (`runnerIdentityProblems`). Green on a7ab175: run 42482060115. The negative control fba64a2 restarted the venv's launcher; run 42481582400 failed with `plugin_runner_broker_client_refused` (client 5516, runner 7864), a refused hello, and `thumb_small/share` falling back to the worker. a7ab175 reverts it.

## What we learned
- Design (b), letting the broker accept a process by Job Object or process tree, would also admit the runner's tools (ffmpeg, ffprobe), which share both. The identity that holds is the pid of the process the launcher created suspended and holds a handle to. The held handle also stops pid reuse.
- The fingerprint runner (no tools, so one process) starts directly as the base interpreter, which proves no second process is needed.
- `plugins / first-party` failed four times in a row on these branches with the classify `detect` N16 budget (plugin p50 about 50 ms against 1.10 × about 25 ms). One run failed `test_a_slower_plugin_fails_n16_with_both_numbers` instead. The same Linux-facing code passed twice before that and passed again on rerun, and main passed meanwhile. It is a timing gate that is sensitive to the runner, not this change.

## Keep doing
- A startup self-check for every identity a sandbox boundary relies on, so a refusal shows at start rather than as each job's fallback.

## Open
- GPU on Windows: no CI runner has a GPU.
- `plugins / first-party`'s timing flake: the classify `detect` N16 budget and `test_a_slower_plugin_fails_n16_with_both_numbers` fail on some shared runners with code that passes elsewhere. No fix or issue yet.
