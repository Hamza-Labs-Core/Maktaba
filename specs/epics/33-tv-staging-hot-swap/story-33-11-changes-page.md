# Story 33.11 — Changes page

## Description

*As an admin, I want one page that shows what is live, what is staged, what exactly would change, whether
it passed its checks, and the buttons to approve, schedule, swap or roll back.*

Design: README §UI, [design.md](design.md) §11.

## Acceptance criteria

- **AC1** Admin → Live TV → **Changes**. A slot bar per host: each slot's role, release, version, state,
  warm channels, encoders used/capacity; a banner when a change is in flight.
- **AC2** List: state chip, kinds, title, source (pipeline job link / editor / settings / upgrade /
  admin), author, created, checks summary (pass/warn/fail), swap time; filters by state; the queue of
  drafts.
- **AC3** Detail tabs:
  - **Lineup**: added, removed, renumbered (`105 → 115`), renamed, definition changes (field-level diff of
    mode, sources, filler, look);
  - **Schedules**: per channel, the divergence time and the next 24 h before/after side by side (titles,
    times), with "unchanged" collapsed;
  - **Settings**: key diff of `rules.tv` / `rules.tv_output` with labels from the settings schema;
  - **Binary & art**: versions with release notes link; art thumbnails before/after;
  - **Checks**: each check per channel with its measured value and rule; failures first;
  - **Preview**: player on the staging URL (33.6) with the staged guide strip; channel picker;
  - **History**: events with actors.
- **AC4** Actions shown from `allowed_actions`: Stage, Re-verify, Approve (with optional swap time,
  swap mode and the param-change acknowledgement when required), Schedule, Swap now, Cancel, Roll back
  (warm with remaining minutes / cold); destructive actions confirm.
- **AC5** Live updates (poll 2 s or SSE) of state, checks progress (`verifying 3/9`), switch progress
  during a swap (`switched 7/9, next 20:30 · 101`).
- **AC6** The channel editor shows "editing draft change #42" and links to it when staging is on.
- **AC7** English and Arabic (RTL), keyboard accessible, works at phone width.

## Test cases

- **TC1** `Changes.test.tsx` — list, filters, allowed actions per state.
- **TC2** `ChangeDetail.test.tsx` — each tab renders fixtures (renumber, add, remove, schedule diff,
  settings diff).
- **TC3** Approve dialog — param-change acknowledgement required when flagged.
- **TC4** i18n keys present in `en.json` and `ar.json`; RTL snapshot.

## Edge cases

- **EC1** A large regenerate (20 channels): the Schedules tab paginates per channel.
- **EC2** Staging disabled: the page explains and shows the release history only.

## Dependencies

33.10; 33.6 for Preview.
