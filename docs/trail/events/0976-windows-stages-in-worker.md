---
schema: 1
kind: pr
pr: 976
title: First-party stages stayed in the worker on Windows until tools started through anonymous pipes (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [956, 974, 977]
---

## What happened
- #956 shipped in v0.3.102. #974's Windows CI then showed that inside its AppContainer a runner cannot create the named pipes asyncio's Windows event loop uses to capture a tool's output (`WinError 5`).
- The stages that start tools with captured output: `engine/media.py`, the thumbnail generator, audio probe and extract, the subtitle extractor, the transcription audio decode and convert.
- Most of these let the `PermissionError` propagate, so the runner reported `sandbox_refused` and the worker reran the job. `engine/media.py` caught `OSError` around the start and returned exit 127, so its stages failed as a tool failure instead of falling back.
- A guard made `native.choose()` keep every first-party stage on Windows in the worker (`windows_tool_pipes`) and start no runner, the behaviour before v0.3.102, until #977 removed it.

## What we learned
- A Windows sandbox test of the sandbox itself (#956's probe) does not exercise the stages' own code paths; a stage started end to end inside the AppContainer would have caught this before the tag.
- A helper that turns an `OSError` into an exit code hides a sandbox refusal from the fallback; refusals must propagate.

## Open
- A real first-party stage driven end to end inside the AppContainer in Windows CI.
