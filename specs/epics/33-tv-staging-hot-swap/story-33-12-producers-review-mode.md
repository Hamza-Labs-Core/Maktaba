# Story 33.12 — Producers in review mode: write_channels, the editor and settings make TV changes

## Description

*As the owner, I want the pipeline's channel writes, my edits in the channel editor and my TV settings
saves to become staged changes instead of landing on the air directly.*

`apply_or_review` (engine/changes.py) remains the seam: with `tv_staging.enabled`, `write_channels`'
`apply` stages a TV change instead of writing `channels`. `write_mode = review` keeps today's parking;
approving the TV change on the Changes page is the queue's Apply.

Design: [design.md](design.md) §11.4.

## Acceptance criteria

- **AC1** `write_channels` with staging on and `write_mode = auto`: `apply` builds a release from the live
  release + the change set and creates a TV change in `staged` (`source = {job_id, stage, plan_id,
  digest}`); `channels`, `filler_pools`/`filler_items` and art are not modified on air; the job's summary
  links the change; it is verified and then waits for an admin's approval (default rules).
- **AC2** `write_mode = review`: the digest is parked as today (`review_digests`), and a `draft` TV change
  with the change set and diff is created so the Changes page shows it; **Approve on the Changes page
  posts `{apply:true, plan_digests:[digest]}`** to the job (`POST /api/queues/write_channels/apply`), the
  re-run stages the change, and the controller treats it as approved (no second approval), subject to
  `require_second_admin`.
- **AC3** (the owner's rule: "when an update happens it needs to start a change; anything after that is
  added to the same change, unless it's approved"; replaces "a changed plan supersedes the draft" and
  "beyond draft takes no more updates") An update to the TV starts a change, and every update after it
  joins that same change - same id, same number, `updated_at` bumped, an `append` event naming the
  update - until the change is approved. Updates are a channel plan from `write_channels`, a schedule
  regeneration (the `stale` flag), a hand edit in the channel editor (channels, art, filler pools and
  clips), a TV settings save and a rollback's re-stage; all kinds share the one open change: the change no
  one approved in `draft`, `staged`, `verifying` or `ready` (`tvrelease/store.py` `open_change`, the API's
  `Store.OpenChange`). A newer plan replaces the older plan's part of the change; hand and filler edits
  made in between are kept, laid over the newer plan field by field (the rules:
  `pipeline/src/maktaba_pipeline/tvrelease/merge.py`, the API's `tvchange/merge.go`). A plan whose digest
  the change already holds is not a new update. An update that joins a change already checked (`staged`,
  `verifying` or `ready`) sends it back to `draft` with a `reopen` event ("updated after it was checked:
  it is checked again"; its old verify report kept in that event as history, its release built anew when
  it is staged again); when the write mode stages by itself (`write_channels` in `auto`) it is staged and
  verified again - one stage asked per update (`diff.stage`, cleared by the stage job and by a failed
  check), so it never loops. Only an approved change (`approved_by` set, or `approved`, `swapping`,
  `live`) takes no more updates: the next update starts a new change, queued behind it. Two open changes
  at once (one opened before this rule, or while the first was in flight - live: #46 `ready` and #47
  `draft`) are brought into line: the newer joins the older (merged by the same rules, ended
  `superseded`, `end_reason = joined_change`, an `append` event on the older), at the controller's next
  tick and at every update (`Store.CollapseOpen`, `stage.collapse_open`; idempotent). A change that fails
  verification is not ended: it returns to `draft` with its report and the reasons (its approval
  withdrawn, no stage asked), is the open change again, and a change opened while it was in flight joins
  it. The owner fixes it, or approves it again. `failed` stays only for the rows made before this rule
  (slot 0142 cleans them up).
- **AC4** `write_channels._verify` checks the release snapshot; the read-back of `channels` runs after the
  swap commit and is reported on the change.
- **AC5** The tool's safety rules hold for staged changes: channels edited by a person since the last write
  are left alone, conflicts are reported (both in the job summary and the change's diff).
- **AC6** Channel editor saves (`/api/channels/*`), filler pool edits and art overrides with staging on write
  to the open change (created if none; AC3); the editor reads draft values; "back to plan" works on the draft.
- **AC7** The `stale` flag with staging on marks the draft channel `sched_changed` and never regenerates the
  live generation.
- **AC8** Settings saves of `rules.tv` / `rules.tv_output` with staging on create/append to the open change
  (the Settings page says "saved to draft change #43"); other sections apply directly as today.
- **AC9** Top-ups write every open generation (33.3) and never create changes; `plan_channels` is
  unchanged; `plex_livetv` runs on the swap (33.7), not on `write_channels` completion, when staging is on.
- **AC10** With staging off, every producer behaves exactly as on `main` (regression tests).

## Test cases

- **TC1** `test_write_channels_auto_stages_change` — no `channels` row changed; change staged.
- **TC2** `test_write_channels_review_creates_draft_and_apply_stages`.
- **TC3** `test_updates_join_one_change_until_it_is_approved`, `test_schedule_regeneration_joins_the_open_change`, `test_merge.py`; Go `TestSaveJoinsTheOpenChange`, `TestApprovedDraftTakesNoMoreUpdates`, `TestFailedVerificationReturnsToTheOpenChange`, `TestMergePendingRules`; until approved: `test_updates_join_a_checked_change_until_it_is_approved`, `test_two_open_changes_collapse_into_the_older`, Go `TestUpdatesJoinACheckedChangeUntilApproved`, `TestTwoOpenChangesCollapseIntoTheOlder`.
- **TC4** `TestEditorSavesToDraft` (Go API).
- **TC5** `TestSettingsSaveToDraft`.
- **TC6** `test_stale_marks_draft_only`.
- **TC7** Regression suite with staging off.

## Edge cases

- **EC1** A pipeline run while a change is in flight: the new draft queues (33.10 AC4).
- **EC2** Edits to a channel the in-flight change also edits: the draft is marked `needs_rebase` when the
  in-flight change goes live.
- **EC3** The `plex_livetv` edge `write_channels -> plex_livetv` is suppressed when staging is on (the swap
  enqueues it instead), so Plex never sees a lineup that is not live.

## Dependencies

33.10; queue engine change sets (`engine/changes.py`), `write_channels`.
