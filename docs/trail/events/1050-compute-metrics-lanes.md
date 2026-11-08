---
schema: 1
kind: pr
pr: 1050
title: compute_metrics runs 4 to 7 times faster with exactly the same outputs
date: 2026-11-08
session: 49a6bef7-3131-4409-9092-75a0b5c5c2fa
---

## What happened
- `production_style.compute_metrics` was the open cost item left by #996. `style_sample` (core-media) calls it once per video, and `rate_style` only reads the stored metrics per season. It runs no SQL.
- Profiling with cProfile on two sets of inputs showed three pure-Python pixel loops. The first set was the parity worlds: what `style_sample` reads from the fake ffmpeg for classify's 28 videos, core-media's 3 files and livetv's 44-video show. The second was a synthetic 30-episode season, half studio talk and half produced.
  - The 16×9 look grid: 720 `sum()` calls per thumbnail, 45 % of the season's time.
  - The edge share: a `map` chain per row, 36 %.
  - The look search: each thumbnail compared cell by cell with every earlier look. This was 43 % on the parity worlds, where the fake's noise thumbnails make every picture a new look.
- The fix stays pure Python (numpy is not a core dependency):
  - Each picture becomes one Python int with a 16-bit lane per pixel (SWAR), and each step is one big-integer operation. No lane can carry or borrow, so the integers are exactly those of the old loops.
  - The look search skips a look whose grid sum is too far off to win, because |Σa − Σb| ≤ Σ|a − b|.
  - Grid and edge share are computed once per distinct picture in each call.
- Results on an M3 Ultra (median of 7 runs, `uv run --extra dev python -m tests.classify.bench_style_metrics`): parity worlds went from 3.37 s to 0.47 s CPU and the season from 1.36 s to 0.34 s. Peak memory rose by under 50 KiB.
- Classify's wave 1 parity test went from 4.91 s to 3.87 s (median of 3 runs, noisy on a shared machine).
- The old code is kept verbatim in `pipeline/tests/classify/style_reference.py`, with its answers on all 105 videos in `style_reference.json`. The new outputs equal them exactly.
- A count of the work and a CPU-time ratio to the reference (at least 2.5×) both fail on the old module. Merged as eb58889fc on head 3340a460a.

## What we learned
- On small fixed-size images, Python's big integers act as a wide vector unit. Packing the pixels into lanes beat `map`/`operator` chains by 3 to 6 times, with no dependency.
- `to_bytes()` + `sum()` adds up lanes faster than the multiply-by-ones trick on these sizes (1.6 µs against 5.8 µs a grid).
- A regression fixture made from random inputs needs stdlib `random.Random` for its draws. numpy's `Generator` streams are not promised stable across versions, so the fixture could break on a dependency bump.
- CI's `plugins` job (the first-party parity suite) skipped this PR, because its path filter does not cover `pipeline/src/maktaba_pipeline/classify`. The suite ran locally instead (277 passed) on a fresh `initdb -E UTF8` cluster.

## Keep doing
- Keep the old algorithm in the tests next to a fixture of its answers. The fixture keeps the full sweep cheap in CI, and the live reference checks the fixture.
- Guard a speed-up with counted work and a CPU-time ratio measured in one process, not with a wall-clock limit.

## Open
- `statistics.pstdev` (exact Fraction arithmetic) is now about 20 % of what's left for a season. Changing it would change the last bits of `luma_std`, `sat_std` and `chroma_spread`, so it stays.
- The `plugins` job's path filter skips changes to shared pipeline code that first-party stages call, such as `classify/production_style.py` for `style_sample`.
