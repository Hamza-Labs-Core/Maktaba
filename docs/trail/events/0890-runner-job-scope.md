---
schema: 1
kind: pr
pr: 890
title: A runner file job moves only its own subject's files (54.32 §2, T02)
date: 2026-11-07
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [844]
---

## What happened
- Issue 796: in a first-party plugin's runner, a `files` job could move any file of the library. The sandbox matches folders, and the worker's checks cover only database writes.
- Before a job of `convert_mp4` (for a video), `dedup_apply` or `file_restore` starts, the runner now reads its subject's files through `scope.*` statements. An audit hook then refuses any other library file with `plugin_scope`. The refusal is kept on the scope, so the job is refused even when the stage's own code catches the error (`_move_file` catches `OSError`).
- Stacked on the merge for PR 844 (wave 5 is in it); it retargets to main after that lands.

## What we learned
- The parity harness runs the `Runner` in the test process and never calls the runner's `__main__`. A guard installed only there never ran in the parity test, and the first hostile case passed for the wrong reason: the foreign file was simply moved. The guard is now installed by the `Runner` on its first scoped job, and the library roots travel with the scope.
- A scope built from the file-action ledger can be widened by the plugin itself, since it writes that table. A forged failed move with a member's `video_id` and another video's path would have been allowed. The group scope reads only `duplicate_members` and `videos`. Convert keeps its own video's ledger rows, because recovery needs the original's path after the database swap.
- Audit-hook `dir_fd` arguments are `-1`, not `None`, when unused (`os.rename`).
- `p.drain(side)` claims the follow-on jobs too. Check the job by the id `enqueue_job` returns, never the newest row of the stage.

## Open
- YouTube's download swap (wave 6) is not scoped; it creates a new video rather than moving an existing one.
