# Story 27.1 — Channel definition (CRUD)

## Description

A **virtual channel** is the root entity of Epic 27: a named, numbered
slot in the user's lineup that, combined with a programming rule and the
library, produces a linear stream. This story defines the channel
record, its CRUD API, and the persistence; the visual builder is
[Story 27.8](story-27-08-channel-admin-ui.md), schedule generation is
[Story 27.2](story-27-02-program-scheduler.md), and serving is
[Story 27.3](story-27-03-live-stream-engine.md).

A channel carries identity and presentation (name, number, logo,
category), a **programming mode** with mode-specific config, an optional
**source filter** (which subset of the library it draws from), and
lifecycle flags (enabled, sort order). It is **library-scoped** and
ACL-gated exactly like collections.

This story owns the **API** (Go, `api/`) and the **data model** (slot
0081). It exposes enough of an admin surface (a basic list + create/edit
form) to be usable on its own; [27.8](story-27-08-channel-admin-ui.md)
replaces that with the full visual builder.

## Channel fields

| Field | Type | Notes |
|-------|------|-------|
| `id` | UUID | PK |
| `library_id` | UUID? | scope; null = multi-library (uses `source_filter` across all readable libraries) |
| `number` | int | channel number, unique within scope; what the user "tunes" to |
| `name` | text | display name |
| `slug` | text | URL-safe, derived from name; stable id for M3U/XMLTV |
| `logo_path` | text? | re-encoded via thumbnail path |
| `category` | text | `movies` \| `kids` \| `documentaries` \| `arabic` \| `music` \| `news` \| `general` \| … (free-form, suggested set) |
| `mode` | text | `shuffle` \| `marathon` \| `schedule` \| `smart_mix` |
| `mode_config` | jsonb | mode-specific (see [27.2](story-27-02-program-scheduler.md)) |
| `source_filter` | jsonb? | reuses the `smart_query` filter shape (genre/library/collection/rating) |
| `transition` | text | `cut` (default) \| `crossfade` |
| `enabled` | bool | disabled channels don't appear in lineup/guide and don't tune |
| `sort_order` | int | lineup ordering (independent of `number`) |
| `created_at` / `updated_at` | timestamptz | |

## Acceptance criteria

- **AC1** `POST /api/channels` creates a channel; `name`, `number`, and
  `mode` are required; `mode_config` is validated against the schema for
  the chosen `mode` (a `marathon` channel requires a `series_id` or
  source resolving to an ordered series; a `schedule` channel requires
  ≥1 slot). Invalid `mode_config` → 422 with field-level errors.
- **AC2** `number` is **unique within scope** — within a `library_id`,
  or within the multi-library (null) scope. A collision → 409.
- **AC3** `slug` is auto-derived from `name` (sanitised, lowercased,
  deduped with a numeric suffix on collision) and is **stable**: editing
  `name` does not change an existing `slug` unless explicitly cleared.
- **AC4** `GET /api/channels` lists channels with optional `?library_id=`,
  `?category=`, `?enabled=` filters, ordered by `sort_order` then
  `number`, and includes a lightweight "now playing" summary (title +
  progress) computed from the schedule (read path, no transcode).
- **AC5** `GET /api/channels/{id}` returns the full record including
  resolved `mode_config` and the count of generated schedule blocks.
- **AC6** `PATCH /api/channels/{id}` edits any field; changing `mode`,
  `mode_config`, or `source_filter` **invalidates the schedule** and
  enqueues a regeneration ([27.2](story-27-02-program-scheduler.md)),
  but does **not** kill an in-flight live session abruptly — the running
  block plays out, the next block uses the new rule.
- **AC7** `DELETE /api/channels/{id}` removes the channel, cascades its
  `channel_programs` and `channel_schedule_state`, and tears down any
  active `channel_runtime`/session (reaper-driven, not a hard kill of a
  watching client mid-segment — the client gets a clean end-of-stream).
- **AC8** `POST /api/channels/reorder` accepts `[{id, number}]` and
  applies the renumber transactionally, rejecting any batch that would
  produce a duplicate number within a scope (all-or-nothing).
- **AC9** `POST /api/channels/{id}/logo` accepts an image, re-encodes it
  through the existing thumbnail path (size/dimension caps, content-type
  sniff), stores `logo_path`, and never stores the raw upload.
- **AC10** All write endpoints enforce `libraryacl`: only a user who can
  **edit** the channel's library (or, for multi-library channels, who can
  edit all referenced libraries) may create/edit/delete; read endpoints
  require **view** on at least the channel's scope.
- **AC11** `enabled=false` removes the channel from lineup, guide,
  exports, and tuning, but **preserves** its definition and schedule.

## Test cases

- **TC1** `test_create_channel_validates_mode_config` — `mode=schedule`
  with no slots → 422; with valid slots → 201.
- **TC2** `test_number_unique_within_scope` — two channels number 5 in
  the same library → second is 409; number 5 in a *different* library is
  allowed.
- **TC3** `test_slug_stable_on_rename` — create "Kids", rename to
  "Children" → `slug` stays `kids`.
- **TC4** `test_slug_collision_suffixes` — two channels named "Movies" →
  slugs `movies`, `movies-2`.
- **TC5** `test_list_filters_and_now_playing` — list with `?category=kids`
  returns only kids channels, each with a `now_playing` summary.
- **TC6** `test_patch_mode_invalidates_schedule` — changing `mode`
  enqueues a regen and marks `channel_schedule_state` stale.
- **TC7** `test_delete_cascades` — delete → `channel_programs` and
  `channel_schedule_state` rows gone; active runtime torn down.
- **TC8** `test_reorder_all_or_nothing` — a batch with a duplicate
  number applies none and returns 409.
- **TC9** `test_logo_reencoded` — uploading a crafted oversized image →
  stored logo is within caps; raw bytes not persisted.
- **TC10** `test_acl_enforced` — a viewer (non-editor) gets 403 on
  create/patch/delete; gets 200 on list/get.
- **TC11** `test_disabled_channel_hidden` — `enabled=false` → absent from
  lineup/guide/xmltv/m3u and tuning returns 409 `channel-disabled`.

## Edge cases

- **EC1 Multi-library channel, partial access.** A `library_id=null`
  channel with a `source_filter` spanning libraries the requesting user
  can only partly read: the channel is listed only if the user can read
  **all** referenced libraries; otherwise it is hidden (not a partial
  view, which would desync the shared timeline).
- **EC2 Number vs. sort_order.** `number` is what the user tunes;
  `sort_order` is lineup display. They can diverge (channel 5 shown
  first). Reorder edits one or the other explicitly.
- **EC3 Logo deletion.** Clearing `logo_path` falls back to a generated
  placeholder (category colour + initials), never a broken image.
- **EC4 Mode switch mid-broadcast.** See AC6 — the change is
  next-block, not immediate, so a viewer isn't yanked between programs.
- **EC5 Empty source.** A channel whose `source_filter` currently
  resolves to zero playable videos is allowed to exist but is flagged
  `degraded` and shows a "no content" slate when tuned; the guide marks
  it accordingly (the scheduler handles emptiness in
  [27.2](story-27-02-program-scheduler.md)).
- **EC6 Deleting a video that a schedule references.** Channel survives;
  the affected `channel_programs` blocks are repaired on the next
  generation pass (gap filled with filler).
