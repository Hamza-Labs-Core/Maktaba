---
schema: 1
kind: pr
pr: 977
title: A Windows runner starts its tools through anonymous pipes (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [976, 974]
---

## What happened
- `maktaba_plugin_runner.winproc`, installed only in a Windows runner after its sandbox, makes `asyncio.create_subprocess_exec` start tools with `subprocess.Popen` and anonymous pipes, keeping asyncio's API (reader threads feed `StreamReader`s, a thread waits for the exit). Each tool runs in a Job Object of its own, so a timeout's kill or a cancel ends it and what it started.
- `DEVNULL` becomes an empty anonymous pipe and a discarded output a pipe a thread drains: an AppContainer may not open the `nul` device either. #974's share routing treats that empty input as no stdin.
- `engine/media.py` now lets a refused start raise `PermissionError`, so the job falls back to the worker; a missing tool is still exit 127.
- #976's guard is removed: first-party stages on Windows run in their runners again. #974's captured thumbnail asserts now expect success, from the share and from a local copy, and pass in the AppContainer.

## What we learned
- An AppContainer may not create a named pipe nor open `nul`: any Windows runner code that opens `os.devnull` directly, not only tool starts, hits the same refusal.
- Close a Job Object handle under the same lock as its terminate, clearing the handle first: closed on the waiter thread while a kill ran on the loop, a reused handle value could have ended another tool (caught in review).
- `Popen` closes the child's thread handle, so a tool cannot be started suspended and assigned to its job before it runs; what it starts in its first instant stays in the runner's own Job Object.

## Open
- GPU on Windows: no device grants; stays open until CI has a GPU.
- A real first-party stage driven end to end inside the AppContainer in Windows CI.
