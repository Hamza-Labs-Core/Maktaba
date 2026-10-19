# Story 33.10 — TV change objects and state machine

## Description

*As the owner, I want every change to my TV to be one thing I can see, approve, schedule, cancel or roll
back, with a history of who did what.*

A **TV change** carries what changed (kinds, diff, the producer's change set), the release it builds,
its verification report, approvals, the swap time and its whole history. The controller in the API runs
the state machine of design §11.2.

Design: [design.md](design.md) §11.

## Acceptance criteria

- **AC1** `tv_changes` and `tv_change_events` (slot 0122); every transition writes an event (from, to,
  actor, source, detail) and an `audit_log` row for admin actions.
- **AC2** States and transitions exactly as design §11.2; an invalid transition answers 409 with the
  current state.
- **AC3** Kinds are computed from the diff (`lineup`, `schedule`, `settings`, `art`, `live_slots`,
  `binary`, `rollback`); the diff lists channels added / removed / renumbered / renamed, changed fields,
  per-channel schedule divergence and next-24 h programme changes, settings key diffs, binary versions.
- **AC4** At most one change in `staged…swapping`; staging another answers 409 `change_in_flight`
  with its id; drafts queue and are re-diffed against the release that goes live.
- **AC5** Approve requires the admin role; with `require_second_admin` the approver must differ from the
  author (or the producer job's enqueuer); `ack_param_change` is required when `push.continuity` asks.
- **AC6** Default: **every** kind (lineup, settings, schedule, art, live slots, binary, rollback) is
  `approval` — no change reaches the air without an admin (owner decision); a test asserts the shipped
  defaults. The rules stay as settings: the strictest rule of the change's kinds applies;
  `quiet_window` promotes at the first minute inside the window with ≤ `quiet_max_viewers` viewers and
  no warnings; `immediate` when ready with no warnings; `approval` never by itself.
- **AC6a** Routine top-ups (extending the existing grid with the same lineup, rules and content
  selection) never create a change; a schedule change (grid, rules, content selection, forced
  regeneration) always does.
- **AC7** `swap_at` schedules the swap; changing it re-anchors the generation (33.3 AC4); a ready change
  past `swap_at` + 1 h without approval stays `ready` and notifies daily.
- **AC8** API endpoints of the README (list, get, patch, stage, verify, approve, promote, cancel,
  preview, rollback, slots, releases) with admin auth; responses include `allowed_actions`.
- **AC9** The controller runs in one API process at a time (advisory lock `tv_change_controller`) and
  resumes in-flight changes after a restart (state is in the database).

## Test cases

- **TC1** `TestTransitionsTable` — every (state, action) pair.
- **TC2** `TestOneInFlight`.
- **TC3** `TestAutoPromoteRules` — kinds × rules × window × viewers × warnings; `TestDefaultsAllApproval`.
- **TC3a** `test_topup_creates_no_change`.
- **TC4** `TestSecondAdmin`.
- **TC5** `TestControllerResume` — kill the API mid-verification; resumes.
- **TC6** `TestDiffKinds` — fixtures of release pairs.

## Edge cases

- **EC1** A producer creates a change identical to live (digest equal): no change is created (`noop`).
- **EC2** Two producers write while a draft is open: both append to the draft; the diff is recomputed.
- **EC3** A change whose base release is no longer live when staged: rebased (diff recomputed); conflicts
  (same channel edited in both) mark the draft `needs_rebase` for the admin.

## Dependencies

33.2, 33.3; used by 33.5–33.9.
