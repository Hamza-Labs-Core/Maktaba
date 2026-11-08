---
schema: 1
kind: pr
pr: 990
title: Share media stays in the worker on Windows; v0.3.105's misses run again (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [977, 986, 988]
---

## What happened
- v0.3.105 (cut 2026-11-08 at #977's merge) runs first-party stages in their Windows runners. On a share, `thumb_small` there ends done in 0 ms with no thumbnail; #986's end to end showed #988 alone does not cure it (no file look was made at all), so the root cause is still in #986.
- `native.choose`: on Windows, while a library root is on a share (a `\\server\share` path or a mapped network drive; unknown counts as a share), every first-party stage whose plugin reads or writes the libraries runs in the worker (`windows_share_library`). The guard covers the job and its fingerprint: `_native_impl` returns before it wires the fp runner. `MAKTABA_WINDOWS_SHARE_RUNNERS=1` asks the runners anyway; only the end to end sets it.
- Every Windows runner now installs the broker shim (`install_unbrokered` when there is no broker), and any share path outside the brokered folders raises `PermissionError` from every look. The runners that write the libraries in full (convert, dedup, repair, youtube) are never brokered, so before this they had no shim at all.
- `reconcile._share_miss_sweep` queues once more, once per subject, results since #977's merge in libraries on a share: `thumb_small` done with `no_thumbnail` (forced: its fingerprint is unchanged, so a plain rerun would be skipped) and `dedup_apply` failed as `keeper_unreadable` (with the approval payload it carried). A `convert_mp4` failed as `file_missing` is queued again by the next completed scan (`missing_files.confirm_missing`).

## What we learned
- A launcher brokers no share for a folder its runner writes in full (`brokerRootsOf`), so on Windows the files and net runners reach no library on a share; code that catches `OSError` (dedup's keeper probe and full hash) records a failure instead of handing the job over.
- A forced rerun is the repair for a wrong "done": the engine skips a job whose input fingerprint matches its last success, and a result read wrong keeps the fingerprint it was read with.
- When a guard's description changes mid-review (here, widened from the full-write groups to every library stage), restate the final scope everywhere it was reported, not only in the PR body.

## Stop doing
- Reporting a fix as the production protection before an end to end on Windows has run it.

## Open
- A Windows end to end of the guarded path (`thumb_small/share-guarded`, override off) in #986.
- The runner-path root cause in #986; once `thumb_small/share` passes in its runner, a later PR can drop the guard for the read-only groups.
