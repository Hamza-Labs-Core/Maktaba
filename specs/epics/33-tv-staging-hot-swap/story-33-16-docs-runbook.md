# Story 33.16 — Docs and runbook

## Description

*As the owner, I want a short guide to run staging day to day and a playbook for when something goes
wrong.*

## Acceptance criteria

- **AC1** `docs/tv/staging.md`: what a release, a slot and a change are; turning staging on; the Changes
  page; approve, schedule, swap modes; preview (browser, VLC, staging HDHomeRun); rollback (warm/cold/auto);
  settings reference (`rules.tv_staging`).
- **AC2** Playbook: verification failed (reading the checks); staging slot crash-loops; live slot crash
  (inside/outside grace); a channel skipped at a swap; Plex shows the old lineup (force `plex_livetv`);
  remote viewers or simulcast outputs reconnected (relay events, param-change policy); front door
  restart; database unavailable; disk full; rollback point expired.
- **AC3** Upgrades with staging (33.14): order, what restarts, expand/contract migrations, Windows notes.
- **AC4** Resource notes: VideoToolbox/NVENC budgets, the budget order live → standby → staging, rolling
  swap, `hot_standby` cost with 22 always-on pushed channels, `standby_warm = viewers`, QSV nodes as
  future capacity.
- **AC5** English; Arabic summary page for the Changes UI.

## Test cases

- **TC1** Docs link check in CI; every setting key in the schema appears in the reference table.

## Dependencies

All stories (written alongside each phase).
