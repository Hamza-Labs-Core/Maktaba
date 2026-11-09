---
schema: 1
kind: pr
pr: 1057
title: The first-party proofs run for the pipeline code their stages import
date: 2026-11-08
session: b7095d77-a4dd-4b22-9468-dcb6f082ae2b
prs: [1050]
---

## What happened
- The first-party parity suite (`plugins / first-party`) runs each plugin's own `entry` on both sides, so it depends on every `maktaba_pipeline` module the plugins import. The `plugins` filter in `.github/ci-paths.yml` covered only `engine/**` and `plugins/**`, and a change to `classify/production_style.py` (in the merge for PR 1050) skipped the suite.
- `tools/ci/firstparty_imports.py` (stdlib only) now derives the coverage from code: `packages_used()` reads the direct imports of `plugins/first-party/*/src/**` and maps each `maktaba_pipeline` module to its package, top-level file or the root `__init__.py`; `stage_files(stages)` follows a named stage's manifest `entry` to the exact files, package `__init__.py` files included.
- The `plugins` filter gained 26 paths; `runner_stages` (the Windows end-to-end run) gained 13, including `plugins/first-party/subtitles/**` and `plugins/first-party/dedup/**`: the Go test also runs `subtitle_gen` and `trash_purge`, whose plugins the filter never listed.
- `tools/ci/tests/test_ci_paths.py` checks both filters against the derivation, with positive controls (removing `classify/**` fails, naming that path). Both checks fail on the old `ci-paths.yml`. update-rehearsal.yml was left as it was: its scenarios prove claims, drain, flip and runners, not what a stage computes. Merged as e4fa2f18f on head dd0ea4c61.

## What we learned
- Only direct imports are a useful filter: the transitive graph reaches 44 of 57 top-level entries, because the engine imports almost everything.
- A hand-written path filter drifts from the code it guards; derive it from the imports and the test tables, and test the derivation in `lint / repo`, which runs on every non-trail PR.

## Keep doing
- When a suite runs code through an indirection (a manifest `entry`, a Go test table), make its path filter follow that indirection in a test, not by hand.
