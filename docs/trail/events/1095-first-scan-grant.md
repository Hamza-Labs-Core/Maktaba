---
schema: 1
kind: incident
pr: 1095
title: "The first scan of a new library walked nothing: the runner had no grant for its root yet"
date: 2026-11-10
session: 1ae75261-b8cb-4a4e-bb1d-0ed6d6aad03f
agent: claude-code
---

## What happened
- **Found by** the NAS CI (story 59.15, PR 1091): a scan asked right after a library is made
  found 0 files (`share_root_unreachable`, EACCES, `files_walked=0`). A scan about 30 s later
  found them all. Not NAS-specific: main had it on every platform with plugin runners.
- **Cause:**
  - The worker writes the runners' file areas (`areas.json`, the folders each runner's sandbox
    grants) only at its 30 s refresh (`refresh_stages`).
  - `native.choose()` sends a files stage to the worker when the runner's hello digest differs
    from the last written areas (`runner_areas_stale`). But a scan right after a new library
    was compared with the areas from before the library existed. Those matched the runner's
    digest, so the scan went to the `light` runner, whose sandbox refused the root.
- **Impact:** a new library looked empty until its next scan, with permission errors in
  server.log.
- **Fix** (merge f08965f63):
  - Before placing a `scan` (`AREAS_FIRST_STAGES`), `choose()` writes the areas afresh and
    compares the runner's digest with that. The cost is one read of `libraries` per scan.
  - The first scan runs in the worker, and so does every files stage after it, until the
    runner says it holds the new areas. Runners still start on demand, and an idle Core stays
    at four processes.
- **Proof:** `test_the_first_scan_of_a_library_just_made_waits_for_the_runners_grant` (fails
  on main), run in `plugins / first-party`.

## What we learned
- A guard that compares against a cached record is only as fresh as that record's refresh.
  Refresh it where the change is most likely: before the job that follows the change.
- `merge-main.sh` kept both CHANGELOG entries, but placed this branch's entry at the end of
  the second `### Fixed` list of `[Unreleased]` (it has two). Read where an entry landed
  after a merge; a misplaced one is valid but hard to find.

## Open
- The NAS CI's strict `first_scan` assertion (`needs: ["scan-grant-race"]` on PR 1091) can
  now be switched on.
