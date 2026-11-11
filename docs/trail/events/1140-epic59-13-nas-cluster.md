---
schema: 1
kind: pr
pr: 1140
title: "A NAS as the home of a cluster: its shares, nodes that join it, and its own paths converted (story 59.13)"
date: 2026-11-11
session: 31bcc09c-71ce-4ad2-b88f-7f6d90623142
agent: claude-code
---

## What happened
- Merged as 7161c1e85c30f7d0fcfd552dd0ed96ecc3731441 (head d2de042b5).
- A NAS home works out the share of each library folder. The rules: DSM `/volume<N>/<share>`, Unraid `/mnt/user/<share>`, QNAP `/share/<share>`, TrueNAS names from setup. It keeps the NAS's own `storage_servers` record (`ensure_home_server`), so a computer joining as a node opens `/Volumes/<share>` with no path map.
- What ships with it:
  - the setup share step;
  - **Machines → This NAS's shares**, with admin routes enforced by role;
  - the `cluster.no_writable_share` notice;
  - an explicit `--light-nas` choice for a Light NAS joining as a node;
  - strings in English and Arabic.
- **Behaviour change:** an existing DSM, Unraid or QNAP home now converts its stored paths to `smb://<nas>/<share>/...` (Epic 31's automatic conversion) once its own record exists and the share map has settled (`storage.map_settle_minutes`). It takes a backup first and can be undone with **Revert last conversion**. A home whose platform is not a NAS makes no record and is never converted.
- The proof in CI is the NAS-shapes cells:
  - **`dsm` cell, `nas_converted`:** the conversion runs. After it, a rescan finds the same 40 videos and adds no duplicates. A clip added later is found at its share path and gets a poster. A transcode of a converted path works. A stored subtitle (`subtitle_files.content`) and an Arabic sidecar still reach the player.
  - **`dsm` cell, `nas_restart_idempotent`:** after a container restart, the same batch stays and nothing is converted again.
  - **`uid-1000` cell, `plain_home_untouched`:** a plain home has no record and every path stays local.
- That proof found two product bugs, both fixed in this PR:
  1. **A stored subtitle blocked the conversion.** A read-only library's database-copy subtitle has a path where no file exists. The conversion's pre-check counted it as missing and stayed `blocked` for good. The check now skips rows with `content`; their paths are still converted (`sharepath/migrate.py`).
  2. **A converted library was never scanned again.** The scan stage runs in the plugin runner, which put the worker's path map on the job context but never made it the active map; the scanner resolves roots through the global `engine.paths.to_local`. So `smb://<nas>/Movies/en` was walked as written: 0 files, nothing removed, nothing new ever found. The runner now activates the frame's map as a `Resolver` (`job_path_map`), and the frame carries the worker's shares and character maps, so found files are stored in the conversion's NFC form.
- The update rehearsal's `nas-4g` leg failed on the server's per-user rate limit (429) while paging 20,000 videos: four of five runs on 59.12's branch, and on 59.13 and 59.14. The harness's API client now waits out a 429 as Retry-After says, at most 180 s.

## What we learned
- A "nothing changed after the rescan" assertion passes on a scan that walked nothing. Prove a scan with a file added after the change (it must be the only new row), and read `files_walked` in the runner's log.
- Process-global state (`set_active_path_map`) does not cross into the plugin runner: only what the job frame carries does. Any stage moved into a runner that calls a module-level resolver needs the frame to carry what that resolver holds.
- A PR image has no transcription component, so the cells make no `subtitle_files` rows. A database-copy row is seeded by SQL, through the pipeline process's own database address and `PG*` environment: the supervisor's address has no password, which lives in a `PGPASSFILE`.
- A rescan settles on the scan queue's `done_24h` count rising, not on the queue being idle; an idle queue is also what it looks like before the job is claimed.
- `POST /api/videos/{id}/process` was a silent no-op in releases (no JobQ); PR 1147 fixed that on main. The cell proves a new file's poster instead of asking for one.

## Keep doing
- Prove a behaviour change on the real image in a NAS cell before merging: the cells found both bugs where unit tests with an in-process map could not.

## Stop doing
- Treating a rate-limited (429) harness read as a flake to re-run: the harness paces itself now. A 429 left after 180 s is a real failure.

## Open
- A home in Docker bridge mode names its record after the container's hostname (`runnervm7aw5e` in CI), not the NAS's network name. A node needs the name it can reach; setup's server field covers it today, but bridge-mode naming deserves its own check.
- `Resolver` character maps reach the runner, but no cell has a share with a character map (`fullwidth`, `sfm`); a TrueNAS cell would cover it.
