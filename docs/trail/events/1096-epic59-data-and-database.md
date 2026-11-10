---
schema: 1
kind: pr
pr: 1096
title: "Epic 59 story 59.02: the data folder and the database on a NAS, quiet hours"
date: 2026-11-10
session: 74f657f3-91eb-416b-94ab-5aa1c6fcd4ab
agent: claude-code
---

## What happened
- Setup's data-folder check (`setup_datadir.go`) refuses a network, FAT/exFAT or FUSE file
  system, naming the type; a merged share (`fuse.shfs`, mergerfs) gets the cache-pool advice;
  spinning disks get the SSD warning; an unreadable disk (ZFS in a container) is asked in
  Settings → This machine (`[server] data_disk`) and counts as spinning on Linux until then.
- The bundled Postgres takes the tier's memory and, on spinning disks, the four disk settings
  as `-c` arguments (`pgbundle/tuning.go`); `[postgres] settings` goes last and wins;
  `max_connections` stays 300.
- Quiet hours: `[schedule] quiet_hours` (tier default 01:00-06:00 on Light and Standard)
  reaches the pipeline as `MAKTABA_QUIET_HOURS`; `engine/quiet.py` gives a heavy job back
  with `JobDeferred` (`paused`, `quiet_hours:<HH:MM>`, `not_before` at the window), so no new
  state was needed. Run now on a job, a queue (`POST /api/queues/{stage}/run-now`) or a
  library sets `run_now` in the payload and clears `not_before`.
- Merged as d6c1f61dd18675b6caf24d74af6300bd595a8fab. The story was coded in one commit
  (4c82039b4) by a session that stopped at a usage limit before verifying it; a second
  session took the branch over, merged main in, reviewed it against the story and added the
  queue page's Run now, which had an API but no button.

## What we learned
- The claim query takes a `paused` job whose `not_before` is NULL, so clearing `not_before`
  is all Run now needs to restart a held job; a manual pause is told apart by
  `pause_requested`, not by `not_before`.
- The Arabic wiki store keeps a summary of each story, so a change to a story's As built
  section regenerates the wiki JSON without a new Arabic text; `--check` says which.
- A default that changes when work runs reaches every harness that starts a real home: the
  update rehearsal's machines got a runner's tier, so quiet hours held 16.5's `dedup_scan`
  and the scenario timed out. The harness's server.toml now sets `[schedule] quiet_hours =
  ""`; older releases keep the unknown key, so it is safe on the previous version too.
- CI's `affected-tests` reads a coverage map: a new test file that drives a changed module
  is not in the map until it is rebuilt, so the gate asks for one of the mapped tests or a
  `No-Test-Change` trailer naming the new file.
- Running `trail check` locally from a worktree while switching its checkout gives a result
  for the wrong tree: run it on its own checkout, or let the lint job's run decide.

## Open
- T09 (Postgres PSS under 512 MB in a `--memory=4g` container with 20,000 items) is 59.15's
  container rehearsal.
- The bundled Postgres's stale `.s.PGSQL.54329.lock` after `docker kill` is fixed on
  `fix/epic59-pg-stale-lock`, apart from this story.
