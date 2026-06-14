# Story 27.8 — Channel management admin UI

## Description

The full **admin experience** for creating and managing channels: the
visual counterpart to the CRUD API ([27.1](story-27-01-channel-definition.md)),
the scheduler ([27.2](story-27-02-program-scheduler.md)), and filler
management ([27.10](story-27-10-filler-bumper-system.md)). An editor can
create a channel, build its programming rule visually (drag content and
collections into time slots, or define filter rules), **preview the next
48 h** of what will actually play, reorder channels, enable/disable them,
and manage the filler/bumper pools used for padding.

This story owns the web admin UI (React, `web/src/pages/Admin/`),
building on the design system and the existing admin surface.

## Acceptance criteria

- **AC1** A **channel CRUD form** creates/edits a channel: name, number,
  logo upload (with live preview, re-encoded server-side), category,
  programming mode, and `transition`. Validation mirrors the API
  ([27.1](story-27-01-channel-definition.md)): duplicate number, invalid
  mode config, etc. are shown inline before submit.
- **AC2** A **programming rule builder** that adapts to the selected mode:
  - **shuffle:** a filter builder (genre / library / collection / rating)
    reusing the smart-query filter UI; a live "matches N videos" count.
  - **marathon:** a series picker (from [26.3](../26-content-intelligence/story-26-03-series-detection.md))
    + order (aired/dvd/filename) + loop toggle.
  - **schedule:** a **visual day×time grid** where the user drags
    content/collections into dayparts (e.g. kids into 06:00–09:00),
    defines the out-of-slot fill, and sees overlaps flagged.
  - **smart-mix:** a daypart-profile picker + genre weighting sliders,
    with a note when Epic 26 classification is unavailable (falls back to
    weighted shuffle).
- **AC3** A **schedule preview** shows the next 48 h the channel will
  actually play — calling the dry-run preview
  (`GET /api/channels/{id}/schedule/preview`, [27.2](story-27-02-program-scheduler.md))
  — rendered as a timeline so the editor sees padding, filler, and
  program boundaries **before** committing.
- **AC4** **Channel reorder** via drag (and explicit number edit) updates
  `sort_order`/`number` through `POST /api/channels/reorder`, rejecting
  number collisions with clear feedback (all-or-nothing).
- **AC5** **Enable/disable** toggles per channel take effect immediately
  in lineup/guide/exports (the definition and schedule are preserved).
- **AC6** **Filler content management** ([27.10](story-27-10-filler-bumper-system.md)):
  designate videos as filler/bumper, organise them into pools (global or
  per-channel), and assign pools + a padding policy to a channel; an
  "auto up-next bumper" toggle.
- **AC7** Editing a channel's **mode/rule/source** triggers a
  regeneration and the preview refreshes; the editor is warned that the
  change applies from the **next** program boundary, not immediately
  (consistent with [27.1](story-27-01-channel-definition.md) AC6).
- **AC8** All actions enforce `libraryacl`: only editors of the relevant
  library see the admin UI and can mutate; the page is not reachable for
  view-only users.
- **AC9** The builder is **forgiving**: a half-built channel can be saved
  as **disabled** (draft) without a valid full schedule; enabling
  requires a valid rule.
- **AC10** **i18n + RTL** throughout, reusing the existing admin i18n.

## Test cases

- **TC1** `test_crud_form_inline_validation` — duplicate number / invalid
  mode config → inline errors; valid → saves.
- **TC2** `test_shuffle_filter_match_count` — building a filter updates a
  live "matches N" count.
- **TC3** `test_marathon_series_picker` — picking a series + order
  configures a valid marathon channel.
- **TC4** `test_schedule_grid_drag` — dragging a collection into a daypart
  creates a slot; overlapping slots are flagged.
- **TC5** `test_smart_mix_fallback_note` — with classification off, the
  smart-mix builder shows the fallback note and still saves.
- **TC6** `test_preview_renders_48h` — preview calls the dry-run endpoint
  and renders programs + padding on a timeline.
- **TC7** `test_reorder_drag_and_collision` — drag reorders; a colliding
  number is rejected with feedback, nothing applied.
- **TC8** `test_enable_disable_immediate` — toggling disable removes the
  channel from the lineup immediately; re-enable restores it.
- **TC9** `test_filler_pool_assignment` — create a pool, add items,
  assign to a channel with a policy.
- **TC10** `test_rule_change_warns_next_boundary` — changing mode shows
  the "applies from next program" warning and refreshes the preview.
- **TC11** `test_acl_admin_only` — a view-only user cannot reach the admin
  page / mutate.
- **TC12** `test_draft_disabled_save` — an incomplete channel saves as
  disabled; enabling requires a valid rule.

## Edge cases

- **EC1 Preview vs. reality drift.** The preview is a dry-run; a note
  clarifies the live schedule may differ slightly if the library changes
  between preview and commit (the committed schedule is regenerated on
  save).
- **EC2 Huge filter result.** A filter matching 50 000 videos shows the
  count without enumerating them; the builder never tries to render the
  full set.
- **EC3 Overlapping schedule slots.** Flagged with the resolution rule
  (later-declared wins / explicit priority) shown, so the editor isn't
  surprised by [27.2](story-27-02-program-scheduler.md) AC5 behaviour.
- **EC4 Logo upload failure.** A rejected image (too large / wrong type)
  shows an error and keeps the previous logo; never leaves a broken
  image.
- **EC5 Concurrent admins.** Two editors editing the same channel →
  optimistic concurrency (on `updated_at`); the later save gets a 409 and
  a "reload, your copy is stale" prompt.
- **EC6 Disabling a channel someone is watching.** Disable is allowed;
  active viewers get a clean end-of-stream / "channel removed" rather
  than a hard cut (consistent with [27.1](story-27-01-channel-definition.md)
  AC7).
- **EC7 Reorder during live viewing.** Renumbering a channel mid-watch
  doesn't drop the viewer's session (the session is keyed by channel id,
  not number); surfing afterward uses the new order.
