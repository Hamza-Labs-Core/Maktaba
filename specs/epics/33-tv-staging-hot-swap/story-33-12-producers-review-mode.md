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
- **AC3** A plan that changed since the review (another digest) produces a new draft; the old one is
  `superseded` (the queue already re-reviews in that case).
- **AC4** `write_channels._verify` checks the release snapshot; the read-back of `channels` runs after the
  swap commit and is reported on the change.
- **AC5** The tool's safety rules hold for staged changes: channels edited by a person since the last write
  are left alone, conflicts are reported (both in the job summary and the change's diff).
- **AC6** Channel editor saves (`/api/channels/*`), filler pool edits and art overrides with staging on write
  to the open draft (created if none); the editor reads draft values; "back to plan" works on the draft.
- **AC7** The `stale` flag with staging on marks the draft channel `sched_changed` and never regenerates the
  live generation.
- **AC8** Settings saves of `rules.tv` / `rules.tv_output` with staging on create/append a `settings` draft
  (the Settings page says "saved to draft change #43"); other sections apply directly as today.
- **AC9** Top-ups write every open generation (33.3) and never create changes; `plan_channels` is
  unchanged; `plex_livetv` runs on the swap (33.7), not on `write_channels` completion, when staging is on.
- **AC10** With staging off, every producer behaves exactly as on `main` (regression tests).

## Test cases

- **TC1** `test_write_channels_auto_stages_change` — no `channels` row changed; change staged.
- **TC2** `test_write_channels_review_creates_draft_and_apply_stages`.
- **TC3** `test_digest_changed_supersedes_draft`.
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
