---
schema: 1
kind: pr
pr: 1190
title: "ci: install the pipeline without torch's CUDA tree, and keep its wheels cached"
date: 2026-11-12
session: 1832bb42-0706-4528-811f-d1e13b3584d1
---

## What happened
- #1190 (merge 36d875c10) stopped CI from downloading torch's CUDA build for the pipeline's
  tests. `uv sync --frozen --all-extras` pulled PyPI's Linux torch (CUDA 13), plus triton,
  cuda-toolkit and the nvidia-* wheels, through the `stt` and `search` extras. No pipeline test
  runs any of them; they use stand-ins. On a slow PyPI night (one 4.3 MiB wheel took 46 s),
  #1163's `unit / unit` was cancelled after 25 minutes in "Install pipeline deps" (run
  43141286584).
- `tools/ci/pipeline-deps.sh` syncs every extra but torch, triton and the lock's `cuda-*` and
  `nvidia-*` packages. `--cpu-torch` (lint only) then installs torch's CPU build at the locked
  version from `download.pytorch.org/whl/cpu` with `--no-deps`, so mypy still checks
  `stt/cohere_cuda.py`. Unit, integration, e2e, perf-ci, lint and the coverage map use it. The
  coverage map now runs `uv run --no-sync`, because `uv run --all-extras` would sync the tree
  back. `.github/ci-paths.yml` lists the script for those jobs. Releases, installers and the
  venv bootstrap export the lock as before: nothing in `deploy/packaging` changed.
- `tools/ci/tests/test_pipeline_deps.py` runs the script against the real `uv.lock` with a
  stand-in `uv`. It fails if a torch upgrade adds a GPU dependency under a new name, and if a
  workflow runs `uv sync|run --all-extras` again.
- Download (wheel sizes from `uv.lock`, Linux x86_64, cp312): 4,070 MB in 181 packages before,
  399 MB in 159 after (lint adds the 187 MiB CPU torch). The largest wheels left out:
  nvidia-cublas-cu12 581 MB, torch 555 MB, nvidia-cudnn-cu13 553 MB, nvidia-cublas 423 MB,
  triton 248 MB.
- "Install pipeline deps" times, from main run 43070447692 to the PR's final run 43148414949
  (head 2e73d68):

  | job | before | after |
  |---|---|---|
  | unit / unit | 23 s (Prepared 187 packages in 22.79 s) | 3 s (165 packages in 2.35 s) |
  | lint / lint | 41 s | 7 s, the CPU torch included |
  | integration / integration | 39 s | 4 s |
  | e2e / e2e | 19 s | 2 s |
  | perf-ci / perf-ci | 37 s | 4 s |

  The after column ran on a cold cache: the new unpruned key was not yet on main.
- The uv cache was saved and restored, but useless. setup-uv's default `prune-cache: true` runs
  `uv cache prune --ci`, which drops every wheel downloaded from an index. Main's entry held
  6 MB, so every run downloaded the whole set from PyPI again. Those jobs now set
  `prune-cache: false` (setup-uv's cache key then loses its `-pruned` part), and main's first
  push saves the wheels.
- CodeQL failed the first head: `py/incomplete-url-substring-sanitization` (high) on the test's
  `"pypi.org" in registry` check. The test and the script now match the lock's registry exactly
  (`https://pypi.org/simple`). All 61 checks passed on 2e73d68.

## What we learned
- A cache that hits is not a cache that helps: read its restored size. A pruned setup-uv entry
  hits on every run and saves nothing.
- `uv sync --no-install-package` skips a package but keeps its dependencies, so torch's small
  pure-Python dependencies (sympy, networkx, ...) stay and the CPU wheel installs with
  `--no-deps`.
- A later `uv run` with no extras leaves the excluded packages alone, but `uv run --all-extras`
  syncs them back: after a partial install, run with `--no-sync`.
- The task's worktree started 4,222 commits behind origin/main. The first edits were made on
  stale workflow files and had to be redone on `origin/main`.

## Keep doing
- Measure from the lock (each wheel's `size`) and from the jobs' own uv lines (`Prepared N
  packages in`), and compare against a named main run.

## Stop doing
- Installing every extra in CI by reflex: install what the tests import.
- Starting work without checking `git rev-list --count HEAD..origin/main`.

## Open
- The coverage map can only be dispatched on main, so its `--no-sync` change was first proven
  after the merge: its run on 930f7aabf (2026-11-14) succeeded.
- Main's first push after #1190 saves the unpruned uv cache (about 0.4 GB, beside the
  repository's 10 GB limit); the next pull requests should restore it.
