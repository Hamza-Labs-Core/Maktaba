---
schema: 1
kind: pr
pr: 565
title: A leaked model thread ended the pipeline test run with exit 0
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge ea593c5ee. Running tests/cluster with the other folders stopped at about 87 % with no summary and exit 0.
- On a Mac with mlx-audio and the Cohere model cached, tests/stages/test_transcribe.py loaded the real second-listen model (11.5 s on the GPU) and kept its non-daemon `cohere_0` thread. A later `_serve` test's exit (`_exit_if_threads_linger`) saw it and called the real `os._exit(0)`.
- Linux CI runs the folders together too (`-m unit`, `-m integration`) and was spared only because mlx-audio is absent there; any leaked thread would have made those jobs report success on a truncated run.
- conftest now refuses the real model load, fails any test that leaves a non-daemon thread running (named), and fails a test that reaches `os._exit` instead of ending the run (forked children still exit). tests/test_conftest_guards.py proves both with a nested pytest run.
- Full suite in one process: 3210 passed, 708 skipped.

## What we learned
- A test that reaches a real `os._exit(0)` ends pytest with success and no summary; a truncated run can look green.
