# Story 26.3 — Series detection

## Description

Group videos that belong to the same show into **series** entities. A
series is the parent of an ordered set of episodes spanning one or more
seasons. Detection is a **library-level debounced pass** (not per-video):
after a batch of videos finishes `classify`, a `series-detect` pass reads
the per-video signals and (re)builds the `series` / `series_episodes`
grouping transactionally.

Detection fuses four signals, in priority order:

1. **Parsed show-name similarity** ([Story 26.1](story-26-01-title-parser.md))
   — the dominant signal. Videos whose normalised `show_name` match (or
   are within a small edit distance) and that carry S/E markers are
   strong episode candidates.
2. **Speaker overlap** ([Story 9.11](../09-library-management/README.md)
   diarization voiceprints) — episodes of the same show share recurring
   speakers; high voiceprint-overlap corroborates a name match and can
   rescue episodes whose filenames are inconsistent.
3. **Poster similarity** — a cheap perceptual hash (dHash) over the
   existing `poster_path` thumbnails; near-identical posters across a
   season boost grouping. (No face recognition; posters only.)
4. **Topic overlap** ([Story 26.2](story-26-02-transcript-topic-extraction.md))
   — shared `primary_topic_id` / high topic-vector overlap is a weak
   tiebreaker.

Output: `series` rows (name, season_count, episode_count, poster,
description — all server-proposed, user-overridable) and
`series_episodes` linking each video to a series with `(season,
episode)` resolved from the parser (with conflict handling).

## Acceptance criteria

- A `series-detect` pass over a library produces `series` rows and
  `series_episodes` links such that videos sharing a normalised show name
  + S/E markers are grouped under one series with correct season/episode
  ordering.
- Grouping is driven by a **weighted score** combining the four signals
  (weights configurable per library; name-similarity dominant). A pair
  is grouped only above a `series_link_threshold` (default tuned so name
  match alone is sufficient, and corroborating signals rescue weak
  names).
- **Name match alone is sufficient** to form a series; speaker/poster/
  topic signals *raise* confidence and *rescue* inconsistent names but
  are never *required*.
- Each series records `season_count`, `episode_count`, a representative
  `poster_path` (most-common season poster), and a `description`
  (NULL in v1; filled by enrichment in
  [Story 26.5](story-26-05-web-metadata-enrichment.md)).
- The pass is **idempotent and incremental**: re-running over an
  unchanged library produces no changes; adding episodes extends the
  existing series rather than creating a duplicate.
- **User overrides win.** A series the user renamed, or a video the user
  manually assigned/removed (`series_episodes.is_user_override`), is
  never reverted by a later automatic pass. Auto-detection fills only
  around the overrides.
- `POST /api/series/{id}/merge` and `/split` let the user fix
  over/under-grouping; merges and splits set override flags so the
  automatic pass respects them.
- Movies (`kind=movie`, no S/E) are **not** forced into series; a set of
  unrelated films stays ungrouped (they may still join collections via
  [Story 26.4](story-26-04-auto-collection-builder.md)).
- The pass is debounced (default 30 s after the last `classify` in a
  batch) and can be triggered on demand via an admin/maintenance action.

## Test cases

- `test_groups_by_show_name` — 10 videos `Show.S01E01..E10` → one series,
  10 episodes, seasons/episodes correct, ordered.
- `test_multi_season` — `Show.S01*` + `Show.S02*` → one series,
  `season_count=2`, episodes partitioned correctly.
- `test_speaker_overlap_rescues_bad_name` — two files with garbled names
  but high voiceprint overlap + same year → grouped; assert the rescue
  came from the speaker signal (name similarity alone below threshold).
- `test_name_match_alone_groups` — episodes with clean names but
  diarization disabled (no voiceprints) → still grouped.
- `test_idempotent` — run the pass twice → second run is a no-op (no
  row churn; assert update count 0).
- `test_incremental_extends_series` — add `S01E11` to an existing series
  → episode appended, no duplicate series.
- `test_user_rename_preserved` — user renames a series, re-run pass →
  name unchanged; new episodes still attach.
- `test_manual_assignment_preserved` — user moves an episode to another
  series → automatic pass does not move it back.
- `test_merge_and_split` — merge two series into one; split one into two;
  override flags set; subsequent pass respects them.
- `test_movies_not_grouped` — 5 unrelated movies → 0 series.
- `test_poster_hash_tiebreak` — two same-named-but-distinct shows (e.g.
  remake vs original) with different posters + years → two series, not one.

## Edge cases

- **Same show name, different shows** (remake vs original; a 2003 vs 2019
  series). Disambiguated by `year`/`airdate` + poster hash + topic
  overlap; produces two series with year suffixes in the proposed name.
- **Specials / episode 0 / OVAs.** `S00E*` and `Special`-tagged episodes
  attach to the series under a season 0; ordering puts them per the
  parser, not interleaved with numbered episodes.
- **Episode numbering gaps** (missing E04). The series records the gap;
  missing-episode detection ([Story 26.10](story-26-10-series-view.md))
  reports it. Detection does not invent the missing video.
- **Absolute vs season numbering** (anime with `E137` and no season).
  When only absolute episode numbers exist, the series uses a single
  pseudo-season and orders by absolute number; flagged in
  `series.metadata.numbering="absolute"`.
- **A movie that is part of a collection but not a series** (a film
  trilogy). Stays as movies; the trilogy surfaces as a smart collection
  (26.4), not a series. (Series = episodic content only in v1.)
- **Library deletion / video removal.** `ON DELETE CASCADE` on
  `series_episodes.video_id`; a series that loses all episodes is garbage
  -collected by the pass (unless user-pinned).
- **Diarization off.** Speaker signal contributes 0; grouping degrades
  gracefully to name + poster + topic.
