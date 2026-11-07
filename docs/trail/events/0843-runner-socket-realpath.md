---
schema: 1
kind: pr
pr: 843
title: A runner binds its socket through a linked folder (macOS)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [833, 842]
---

## What happened
- On macOS, a plugin runner whose socket fell back to `/tmp` (a data folder long enough that the socket path passes 100 bytes) could not bind it.
  - Seatbelt matches real paths, and `/tmp` is `/private/tmp`.
  - The runner exited 1 about 10 s after it started, and its stages ran in the worker.
- `sandbox.policy_for` now names the sockets and the run folder by their real paths, as it already did the areas.
- Two tests: the policy resolves a linked folder, and a sandboxed child binds its socket through a linked folder under `/tmp`.

## What we learned
- The sandbox's own tests passed resolved paths (`Path.resolve()`), so the path a home actually gives the runner was never tried.
- The update rehearsal's homes have long data paths, so on a Mac they take the `/tmp` fallback that most real homes never do. It found this right after #833.

## Keep doing
- Give a sandbox test the path the caller gives, unresolved: a link in it is a case of its own.

## Open
- None.
