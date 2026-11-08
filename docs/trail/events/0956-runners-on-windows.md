---
schema: 1
kind: pr
pr: 956
title: First-party plugin runners on Windows, each group in its own AppContainer (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [863, 879, 925]
---

## What happened
- Maktaba's own plugin runners now run on Windows in their runner processes, as on macOS and Linux. Before, `RunnersHere` excluded Windows and the stages ran in the worker.
- A Windows process cannot put itself into an AppContainer, so the supervisor starts each runner through a hidden launcher, `maktaba-server runner-sandbox-run` (`appcontainer.LaunchRunner`).
  1. **Policy:** the launcher asks the runner for its policy (`--policy`, the same `policy_for` the other systems use, printed as JSON; no plugin code is imported).
  2. **Grants:** it grants the group's own profile (`Maktaba.Runner.<group>`) read and write on the folders the policy names, and revokes what an earlier start granted that the policy no longer names (a grants file kept outside the runners' run folders).
  3. **Start:** it starts the runner with no capabilities, or `internetClient` alone for a group with `net`, in a Job Object that allows child processes only to a group with tools.
- Policy paths are compared as the filesystem names them (links and short names resolved). Device names, streams and verbatim prefixes are refused. Each path is resolved again just before its grant. A grant that holds or sits inside a protected folder (the supervisor's token, the keys, other run folders), or is a drive root, stops the start.
- Inside, the runner checks its token is an AppContainer's, with no capability beyond `internetClient` for a `net` group; otherwise it exits 70 and never runs unsandboxed. An audit-hook allow-list refuses programs other than the group's tools.
- Transport: the supervisor serves every pipe, under a prefix random per run, and the runner only dials them.
  - **Worker pipe:** its DACL admits the user only. A broker pairs each worker connection with a runner connection waiting on the back pipe; with no runner waiting within 2 s, the worker runs the job itself.
  - **API pipe:** relays to the plugin API's socket.
- CI's windows-2022 plugins job runs a probe inside the real AppContainer (`TestARunnerOnWindowsInItsAppContainer`).
  - It shows the granted folders only, no loopback or LAN canary without `net`, no child process, and no other group's pipe.
  - The same probes run outside the sandbox all succeed, as a positive control.
- The first CI run failed three ways, all fixed before the merge:
  - the runner's token check declared no ctypes argument types, so the 64-bit process handle overflowed a C int (only real Windows sees it; the unit tests mock the call);
  - a supervisor test compared a `/tmp` path that comes out with backslashes on Windows;
  - the worker's pipe test sat in a test file the affected-tests gate does not map to `native.py`.

## What we learned
- Declare `argtypes` and `restype` for every Windows ctypes call that takes a handle. An undeclared argument is a C int, and `GetCurrentProcess()`'s -1 does not fit as a 64-bit HANDLE.
- The affected-tests gate maps a source file to its tests from main's coverage, not from imports: a new test file importing a module does not count for it.
- A child agent woken by a CI failure on its PR fixes it in its own worktree. The parent should check the child's thread before editing that worktree, or the two collide.

## Open
- GPU on Windows: no device grants, so an `ml` runner may fall back to CPU or report its stage unavailable; CI has no GPU.
- Media on SMB shares is never granted (an AppContainer cannot reach SMB): those jobs run in the worker (`sandbox_refused`).
- A brief window remains between the last path check and the grant, reachable only by a process running as the same user while the group's runner is stopped.
