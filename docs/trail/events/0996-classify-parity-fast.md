---
schema: 1
kind: pr
pr: 996
title: Classify's parity proof ran past the soft cap and failed unrelated pull requests
date: 2026-11-08
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- The classify plugin's parity proof (story 54.31) took about 10 s for its wave 1 case on main: twice the 5 s integration soft cap. On slow runners it went past the 15 s hard cap and failed pull requests that never touched classify.
- A profiler showed three costs, none of them classify's own work:
  - The fake ffmpeg in `tests/stages/world.py` built each style window's thumbnails one `getrandbits(8)` call per byte (92,160 a window, every window, every video, both sides).
  - `go build`'s cache check of the plugin API's dev server ran on every parity open (about 0.6 s).
  - N16's bench re-ran check_tags, check_clips and books 11 times a side in wave 1's library, where the model and the encyclopedia are off and each returns before any work.
- The fixes:
  - The fake bytes now come from one `getrandbits(32 * n)` call that gives the same bytes and leaves the generator in the same state, kept per (family, call). A unit test pins them to the old draws.
  - The dev server is built once per process, in classify's setup fixture.
  - Wave 1 no longer benches the three model stages; wave 4 benches them doing their work.
- Wave 1 went from 10.4 s to 4.0 s and wave 4 from 5.0 s to 3.8 s, on the same machine under the same load. No parity check changed.

## What we learned
- Profile before trimming a slow test. Most of this one's time was in shared fixtures (fake media, a tool build), not in the code under test, so the fix speeds up every test that uses them.
- A fake that must stay byte-identical can still be made fast. Here `getrandbits(8)` is the top byte of one 32-bit draw, and `getrandbits(32 * n)` is n such draws, little-endian.
- A bench only measures something where the stage does its work. Benching an early return costs time and proves nothing that the other case doesn't.

## Keep doing
- Build tools and clone databases in a test's setup, not in its timed call. The soft cap judges only the call.

## Open
- `production_style.compute_metrics` (pure Python, O(frames × looks)) is now the largest CPU cost left in any parity case that drains style_sample: about 0.7 s for classify's 30 videos. It's production code, so it's worth its own look.
- Every parity open still clones two databases and starts the dev server (about 1 s). A shared per-session server would cut that for every plugin's parity.
