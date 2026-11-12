---
schema: 1
kind: pr
pr: 1191
title: "ml runners let idle models go and keep MLX's cache capped"
date: 2026-11-12
session: 1b9099a3-0043-4cb7-9bd3-eebd6ebed39d
---

## What happened
- The investigation began right after a cleanup of leftover test processes that had run the
  home Mac out of memory. With those gone, the two ml runners were still the largest processes
  on the machine, beside a 4.4 GB `llama-server`.
- On the home Mac (96 GB), `plugin-runner/ml-1` and `ml-2` each showed about 9-10 GB in `top`
  after 18 hours, most of it compressed, with swap full; each used about one core.
- Read-only `footprint` of the live runners: 6.3-7.5 GB IOAccelerator (MLX: Whisper turbo,
  about 1.6 GB, and the second listen's Cohere model, about 3.8 GB, plus MLX's cache of freed
  buffers) and 0.9-1.7 GB of large mallocs (the files in flight). Snapshots over 15 minutes
  stayed between 9.0 and 9.9 GB: a plateau, not growth. `sample` showed the time in
  `mlx::core::eval` and scipy's median filter (word alignment): real decoding.
- The runners were not idle: in 24 hours ml-2 ran 3,337 transcribe and 3,453 second_listen jobs,
  each on a distinct video (the `pipeline.stt-2` dispatch log).
- A local repro with the real models (12 files of 3-17 minutes in one process) kept MLX's active
  memory at 5.80-5.86 GB after every job and the footprint at 6.9-7.9 GB: no per-job leak. The
  memory was the planned working set, never given back.
- Root cause: not a leak. Each ml runner holds the working set the plan allows it during the
  backlog (both models, the files in flight, MLX's cache up to its cap), two copies on a
  machine with two transcription slots, and before this change kept it forever, idle or not.
  Two smaller faults added to it:
  - MLX's cache cap ordering: only Whisper set MLX's memory and cache limits, so a runner whose
    first job was a second listen ran with MLX's default cache limit (about 1.5x the GPU's
    working set) until Whisper loaded. The second listen never trimmed the cache either.
  - The spectrogram kept alive: the GPU thread's loop variable kept the last finished file (its
    whole spectrogram, on the GPU) referenced until the next file finished. The test that
    collects it while the thread waits fails on the code before #1191.
- #1191 (merge 9ece66e23): after `MAKTABA_STT_UNLOAD_IDLE_S` (default 600) without work, Whisper's
  GPU thread drops its model and ends, and a reaper lets each idle MLX Cohere recogniser drop its
  weights. The repro went from 7.46 GB to 0.98 GB idle, and the next file reloaded both in 7.7 s.
  MLX's limits are now set by whichever model loads first (`stt/mlx_memory.py`); the second
  listen trims MLX's cache after each call; a finished file's spectrogram is no longer held by
  the GPU thread's loop variable.

  | Local repro, real models | before (main) | after (#1191) |
  |---|---|---|
  | Footprint after each job (3-17 min files) | 7.0-7.9 GB (jobs 1-8) | 6.9-7.8 GB (jobs 1-12) |
  | MLX active memory after each job | 5.81-5.86 GB | 5.80 GB |
  | Idle 30 s (Whisper let go) | kept forever | 5.17 GB |
  | Idle 45-90 s (both let go) | kept forever | 0.98 GB, MLX active 0.00 |
  | Next job after idle | n/a | 7.7 s, both reloaded |

  The repro ran with `MAKTABA_STT_UNLOAD_IDLE_S=30`. The live runners stayed at 9.0-9.9 GB:
  they were in a continuous backlog, which #1191 does not shrink.
- The first version dropped an idle recogniser from the registry, and a caller still holding it
  failed on its next call. The real-model repro caught it: Whisper's cold load took longer than
  the 30 s test timeout. The recogniser now stays registered and reloads its weights for the next
  call.
- `affected-tests` blocked the first push: the tests were in a new file, and the gate maps tests
  to a module through imports in the existing test files.

## What we learned
- On Apple Silicon, `footprint` splits a Python ML process into IOAccelerator (MLX/Metal) and
  malloc. MLX's `get_active_memory`/`get_cache_memory` after each job tell a leak from a working
  set in one run.
- MLX's cache limit is per process, and its default is about 1.5x the GPU's working set: every
  model that can load first must set the limits.
- Unloading a shared model must never break a holder: reload on next use, don't drop the object.
- A runner busy with a backlog looks idle from outside. Its job log (`plugin_runner_job`) and the
  worker's dispatch log answer "busy loop or real work" quickly.

## Keep doing
- Reproducing with the real models and measuring per job before changing code; the repro also
  found the bug in the first fix.
- Putting a module's tests in its existing test file, so the affected-tests gate sees them.

## Stop doing
- Treating a runner's size after many hours as a leak without checking whether it is flat.

## Open
- During a backlog both ml runners still hold the full working set, about 9-10 GB each, two
  copies of the same models. Sharing one model per machine, or a smaller top tier
  (`AudioHours 16`, `GPUGB 20`), is a supervisor decision for another pull request.
- Why the home transcribes about 6,700 videos a day for many hours: check whether a fingerprint
  change queued the whole library again.
- The `light` runner was at 2.8 GB RSS and 62% CPU during the same check.
