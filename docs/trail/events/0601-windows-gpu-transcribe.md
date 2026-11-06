---
schema: 1
kind: pr
pr: 601
title: The Windows NVIDIA node never transcribed: six causes
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 787cfab3f. The Windows node has an RTX 2080 Super but never transcribed, so every transcribe job ran on the Mac. Six causes combined:
  1. GPU detection used NVML only, with no nvidia-smi fallback.
  2. STT was counted only through mlx_whisper.
  3. Transcribe waited for the second listen, which an 8 GB card cannot run on Windows, and the saved reason said only "unavailable".
  4. The gpu pool size came from one global setting.
  5. Transcribe did not check that the machine can read the share.
  6. The node installers installed no extras by default.
- Fixes: detection shares one probe with venv_bootstrap and falls back to nvidia-smi; STT is detected through mlx or ct2 and the node reports which; Machines shows the full reason in English and Arabic; each node sizes its gpu pool from its own card; transcribe is gated on the share read check; the installers and updater add the stt extra on a qualifying NVIDIA GPU. docs/hardware.md now calls this a detection bug.
- node-packaging now restores the cached static nfpm instead of building it from source each run.

## What we learned
- affected-tests only accepts tests from its coverage map, so the stage tests went into tests/cluster/test_stages.py.
