---
schema: 1
kind: pr
pr: 1103
title: "nas-shapes: 59.02's cells (Postgres PSS, the quiet-hours hold) and first_scan"
date: 2026-11-10
session: d29865cb-5183-4329-8a31-fdc09e432a9e
agent: claude-code
---

## What happened
- `landed` gained `59.02` (#1096, d6c1f61dd) and `scan-grant-race` (#1095, f08965f63). Merged in
  f4a9a1c4c with every cell green. Each new check's `report.json` was read to confirm it ran:
  - `mem-4g` runs `postgres_pss_512m`, the sum of `Pss` from `smaps_rollup` over every
    `postgres` process: 137 MiB over 38 processes on the Light tier, against a 512 MiB limit;
  - the new `quiet-hours-hold` cell (4 GB, `TZ=UTC`) gets a one-hour window opening three hours
    ahead. `quiet_hold` checks three things: the first scan waits (`GET /api/queues` says
    `quiet_hours` and `quiet_at` 19:00, no clip found), the admin's
    `POST /api/queues/scan/run-now` runs the held jobs, and then all 40 clips are found;
  - `uid-1000`'s `first_scan` runs: 40 clips on the first scan.

## What we learned
- On its first run `quiet_hold` caught the scan-grant-race (#1095): Run now dispatched the held
  scan, but `files_walked=0`. The runner gets a new root's grant about 30 s after the library
  is made, and the scan was started 15 s after.
  - The assertion now checks that Run now runs the held jobs, then finds the clips as `scan`
    does. The strict check, every clip on the first scan, is `first_scan`'s job.
- A fix's pull request may leave its own `landed` entry out. #1095 merged without
  `scan-grant-race`, so the next harness pull request adds it.
- `uv run --with ruff` at the repo root writes a `uv.lock`, and `git add -A` picked it up; the
  ci-paths guard failed on it. Run tools from the scratchpad, or stage only the files you
  changed.

## Open
- Still waiting: `logs_clean` (#1097, `plugin-cache-grant`), `thumbnail` (#1098,
  `poster-queue`), `media-none` and the access checks (59.03 #1092), and `isolation_container`
  (59.07).
