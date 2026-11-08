---
schema: 1
kind: pr
pr: 988
title: A share file a runner's sandbox refuses runs in the worker, never as missing (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [977, 986]
---

## What happened
- #977 put first-party stages back in their Windows runners. The end to end in #986 found two refusals read as absent files: the broker shim's `exists`/`isfile`/`isdir` answered False for a stat the broker refused, and `unicode_open.open_fallback` turned a refusal into `FileNotFoundError`.
- #988 makes all four raise the `PermissionError` (a missing file still answers False), routes `os.path.lexists`, and refuses `os.scandir` on a brokered folder, so the runner answers `sandbox_refused` and the worker runs the job. It was split out of #986 to land ahead of the next tag; the release session cut v0.3.105 at #977's merge first, so v0.3.105 shipped #977 without it.

## What we learned
- On Windows, `os.path.exists`, `isfile`, `isdir` and `lexists` are `nt._path_*` and answer False on any error, with no `os.stat` call and no audit event: in a sandbox, a refusal reads as a missing file unless each one is wrapped.
- `glob` and `os.walk` swallow `OSError`: a refused folder scan reads as empty even when `scandir` raises. A stage that lists a share folder uses `os.listdir`.
- #988 was correct but not sufficient: on its exact code the end to end still ended `thumb_small/share` with no thumbnail and no broker call (see the #990 event).

## Keep doing
- Split a production fix out of a test PR the moment the test finds it, and merge it fail-closed on its own.

## Open
- The root cause of the share thumbnail miss in the runner: #986.
