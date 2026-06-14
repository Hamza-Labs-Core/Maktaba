# Story 26.4 — Auto-collection builder

## Description

Automatically propose and maintain **smart collections** based on the
classification signals from Stories 26.1–26.3: topic/genre clusters,
content type, language, decade/year, and recurring speaker. These extend
the existing Epic 7 smart-collections feature
([Story 7.14](../07-api-server/README.md), `collections` /
`collection_items` with `smart_query` JSONB) rather than forking it: an
auto-collection is a `collections` row with `is_smart = true` and a new
`origin = 'auto'` discriminator. Because they're query-based, they stay
current as the library grows — no membership table to maintain.

The server **proposes**; the user disposes. Proposals land in
`collection_suggestions` and surface in the UI as cards the user can
**accept** (promote to a real, visible collection), **dismiss** (hidden,
never re-proposed), or **rename** before accepting. Accepted
auto-collections behave exactly like hand-built smart collections.

A library-level **auto-collection pass** runs debounced after a batch of
videos finish `classify`/`enrich`, computes candidate groupings, and
reconciles them against existing suggestions and accepted collections.

## Collection rule kinds (v1)

| Kind | Example proposed name | `auto_rule` (drives `smart_query`) |
|------|-----------------------|-------------------------------------|
| topic | "Topic: Tafsir" | `{by:"topic", topic_id:…}` |
| content_type | "Lectures" | `{by:"content_type", value:"lecture"}` |
| language | "Arabic" | `{by:"language", value:"ar"}` |
| decade | "From the 2010s" | `{by:"decade", from:2010, to:2019}` |
| speaker | "Featuring <speaker>" | `{by:"speaker", speaker_id:…}` |

Each rule compiles to the **existing** `smart_query` filter shape so the
existing collection-serving path resolves membership unchanged.

## Acceptance criteria

- The auto-collection pass emits `collection_suggestions` rows, each with
  a `kind`, an `auto_rule` (JSONB), a proposed `name`, a
  `member_count_estimate`, and a `score` (how strong/useful the cluster
  is).
- A suggestion is only emitted when it clears thresholds:
  `min_members` (default 5) and a per-kind `min_score`, so the UI is not
  flooded with one-video "collections".
- **Accept** (`POST /api/collections/suggestions/{id}/accept`) creates a
  `collections` row with `is_smart=true`, `origin='auto'`, and
  `smart_query` compiled from `auto_rule`; the suggestion is marked
  accepted and links to the created collection.
- **Dismiss** records the dismissal (persisted like
  [`recommendation_dismissals`](../14-discovery/README.md)) so the same
  cluster is **never re-proposed**; dismissals survive re-runs and sync
  across devices/sessions.
- **Rename before accept** (`PATCH …/{id}`) sets the name used on accept;
  the user's name is preserved on the created collection.
- Accepted auto-collections are **live/query-based**: adding matching
  videos later updates membership automatically (via the existing
  smart-query serving path), with no re-accept.
- The pass is **idempotent**: re-running over an unchanged library adds
  no new suggestions and re-proposes nothing already accepted or
  dismissed.
- Auto-collections never silently shadow a user's hand-built collection
  with the same name (`origin='manual'` wins; the auto one is suppressed
  or suffixed).
- Per-library opt-out via `settings.collections.auto` (default on).

## Test cases

- `test_topic_collection_proposed` — library with a dense Tafsir topic
  cluster (≥5 videos) → one `topic` suggestion with correct `auto_rule`.
- `test_below_min_members_not_proposed` — a topic with 3 videos → no
  suggestion.
- `test_accept_creates_smart_collection` — accept a suggestion → a
  `collections` row (`is_smart`, `origin='auto'`) exists; its
  `smart_query` resolves the expected members via the existing serving
  path.
- `test_dismiss_prevents_reproposal` — dismiss, re-run pass → suggestion
  not re-emitted.
- `test_rename_then_accept` — rename a suggestion, accept → collection
  carries the user's name.
- `test_live_membership` — accept a language collection, add a matching
  video later → it appears in the collection without a re-run.
- `test_idempotent_pass` — run pass twice → no duplicate suggestions.
- `test_manual_name_collision` — a manual collection named "Lectures"
  exists → the auto pass does not create a competing "Lectures"
  (suppressed or suffixed), asserted.
- `test_decade_and_speaker_rules` — decade and speaker suggestions
  compile to correct `smart_query` and resolve correct members.

## Edge cases

- **Overlapping memberships.** A video legitimately belongs to "Arabic",
  "Lectures", and "Topic: Tafsir" at once — that's expected; collections
  are non-exclusive views, not buckets.
- **Cluster churn.** When the Story 9.9 topic model recomputes and a
  topic's membership shifts, accepted auto-collections (query-based)
  follow automatically; *pending suggestions* are recomputed and stale
  ones expire.
- **Speaker rename.** A speaker the user named (Story 9.11) flows into
  the proposed collection name ("Featuring Sheikh X"); an unnamed speaker
  yields "Featuring Speaker 3" or is suppressed below `min_score`.
- **Tiny libraries.** Below `min_members` everywhere → zero suggestions,
  which is correct (no empty-state spam).
- **Decade for unknown years.** Videos with no `year`/`airdate` are
  excluded from decade collections, not bucketed into a "1970s" default.
- **Dismissed-then-grown cluster.** If a dismissed cluster later grows
  substantially (e.g. 5×), the pass may re-surface it once with a
  `was_dismissed` flag (configurable; default still suppressed) — the UI
  decision lives in [Story 26.6](story-26-06-enrichment-ui.md).
