---
schema: 1
kind: pr
pr: 665
title: A new dictionary or ffmpeg pack is used without a restart
date: 2026-11-06
session: f0e36dc6-64c3-44f2-980b-fac44d831b70
issue: 637
---

## What happened
- The pipeline kept the ffmpeg and CAMeL dictionary of the packs it started with: the supervisor passes `MAKTABA_FFMPEG` and `CAMELTOOLS_DATA` resolved into a pack's version folder, and `engine/media.py` read ffmpeg once at import (and several stages imported that constant).
- `pack_paths.follow()` moves a path inside `<components>/<pack>/<version>/` to the version `current` names now; the binaries, `media.ffmpeg()`/`ffprobe()` and the dictionary use it at each call, so the next video uses a pack installed or updated meanwhile.

## What we learned
- `binaries.py` already read the pack at call time, but only when `MAKTABA_FFMPEG` was unset, and the supervisor always sets it (to the pack's versioned path): the fallback never ran under the supervisor.
- `from ..media import FFMPEG` binds at import; a module `__getattr__` keeps `media.FFMPEG` reads current, but the `from` imports had to become calls.
- CAMeL Tools reads `CAMELTOOLS_DATA` when `camel_tools.data` is imported, so changing the variable is not enough: the dictionary loads the new folder's `morphology.db` by path.
