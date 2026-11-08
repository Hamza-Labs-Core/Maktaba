---
schema: 1
kind: pr
pr: 1019
title: Thumbnails, chapter frames and subtitles run in their runner on a Windows share; the broker serves full-write shares read-only (story 54.23)
date: 2026-11-08
session: 6e034a77-9c8e-480c-9432-d2d753e4f219
prs: [986, 1018, 1026]
---

## What happened
- #1019 lifts the Windows share-library guard (`windows_share_library` in `native.choose`) for three stages only, listed in `SHARE_RUNNER_STAGES`: `thumb_small`, `chapter_thumbs` and `subtitle_gen`. Every other library stage on Windows still runs in the worker while a library is on a share, unless `MAKTABA_WINDOWS_SHARE_RUNNERS` is set.
- The file broker no longer skips a share that a group may write in full. It serves that share to the group as a read-only root (`write_named` false), so ffprobe in the files runner opens its input there through the broker, and the AppContainer is never granted the share. A write op (write, replace, remove) on such a root is refused with errno 13 and logged as `plugin_runner_broker_write_refused`, and the job goes to the worker as `sandbox_refused`.
- The Windows e2e (runner-stages, `\\localhost\mkstages`) gained share cases with the override off, checked by per-stage verdicts:
  - `thumb_small/share`: broker open:2, stat:2.
  - `chapter_thumbs/share`: open:2, two chapter frames written.
  - `subtitle_gen/share`: stat 1, write 2, replace 2.
  - `convert_mp4/share`: a dry run in the files runner with open:3.
  - `trash_purge/share`: the remove is refused and logged, the job counts remove_refused, and the worker purges the file.
  - `convert_mp4/share-guarded`: stays in the worker.
- A Python test (`test_a_stage_leaves_the_share_guard_only_with_its_end_to_end_case`) refuses a stage in `SHARE_RUNNER_STAGES` that has no override-off share case in the e2e, or whose plugin writes the libraries in full. It also requires the e2e to keep one guarded case.
- Merged as d5f5fd6ae from head 3b5483d, after main was brought in at 7fe991562 with `tools/dev/merge-main.sh`.
  - Green on 3b5483d: run 42486586590, runner-stages job 128749885610.
  - The negative control c34814d emptied `SHARE_RUNNER_STAGES`. Run 42486157235 failed for each lifted stage: the worker chose inproc (windows_share_library), `run_native` was never called, a fallback was counted, no plugin token was minted, and the light runner's log never named the job.
  - 3b5483d reverts the negative control.
- What stays guarded, and why:
  - **convert_mp4, dedup_scan, dedup_apply, repair_broken and youtube_fetch** write the libraries in full. The broker serves their shares read-only, so their writes would be refused and handed back to the worker. They need a write broker before they can leave the guard.
  - **trash_purge and file_restore** move or remove library files. trash_purge/share is the e2e's proof that a write is refused and logged.
  - **transcribe and second_listen** have no e2e case in their runner on a share yet, and a stage leaves the guard only with one.

## What we learned
- A read-only root is enough for a probe: a tool reads its one share input through the `fd:` handle the shim passes on stdin, so ffprobe needs no grant.
- subtitle_gen's summary lists whole Windows paths, so a verdict that compares file names must compare base names with backslashes turned into slashes (84c693e).
- Tying the stage list to the e2e source in a unit test keeps a stage from being lifted on a claim alone.

## Keep doing
- Lift a guard one stage at a time, each with an override-off e2e case whose verdict fails on any fallback, and a negative control reverted in its own commit.

## Open
- GPU on Windows: no CI runner has a GPU.
- A write broker for the full-write groups (convert, dedup, repair, youtube), so they can leave the share guard.
- Runner proof for transcribe and second_listen on a share.
