---
schema: 1
kind: pr
pr: 1100
title: "nas-shapes: the 59.01 cells and restart-after-kill run (story 59.15)"
date: 2026-11-10
session: d29865cb-5183-4329-8a31-fdc09e432a9e
agent: claude-code
---

## What happened
- `tests/nas/cells.json`'s `landed` gained `59.01` (#1090, 2228aa84b) and `pg-stale-lock`
  (#1094, 15b032d88). Merged in 6093314cc with 19 cells green.
- These cells now run, each on an image as a NAS would start it:
  - `uid-568`, `uid-unraid` (99:100) and `uid-dsm` (1026:100, data owned by root and
    group-writable) on amd64 and arm64: ready, every process as that uid, the scan;
  - `uid-unnamed` (4321, no passwd line): nss_wrapper names it, Postgres starts;
  - `data-denied` and `privileged`: the container exits 78 with its message, the Arabic line
    included;
  - `restart-after-kill`: after SIGKILL the same volume starts again and scans.
- Headless setup in every cell now passes `--bundle core`, which the harness had held until 59.01
  landed.

## What we learned
- A cell job passes when its cell is still pending, so a green check alone does not show the
  cell ran. Read each cell's `report.json` in its artifact (`nas-cell-<cell>-<arch>`). For this
  pull request every new cell's assertions were there and passed.
- The harness was ready ahead of the stories (`bundle()` keyed on `landed`, the refusal
  assertions written against 59.01's spec). Turning the cells on was a one-line change to
  `landed`.
- The `trail check` lint wants a pr event named `NNNN-slug.md` with `kind: pr`; the `d…` names
  are for events with no pull request.

## Keep doing
- Turn on each waiting check in the pull request after its fix merges, and keep the README's
  waiting table to the fixes still open.

## Open
- Still waiting: 59.03 #1092 (`media-none`, `access_ro`, `convert_409`), 59.02 #1096
  (`postgres_pss_512m`), #1095 (`first_scan`), #1097 (`logs_clean`), #1098 (`thumbnail`), 59.07
  (`isolation_container`).
- `offline` (ready with no network, 59.01) and `bridge` (59.08) need a harness that reaches the
  API without the host network; they come with a later phase.
