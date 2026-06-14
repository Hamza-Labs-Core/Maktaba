# Story 26.10 — Cross-library series view

## Description

A dedicated **series browser** that presents all detected series
([Story 26.3](story-26-03-series-detection.md)) across **all** the user's
libraries in one place, with:

- A grid of series (poster, name, season/episode counts, library badge)
- A series page with a **season → episode grid**, each episode showing
  its thumbnail, title (parsed or enriched), duration, and **watch
  progress**
- **Missing-episode detection**: gaps in the season/episode sequence are
  shown as ghost cells ("Episode 4 — missing")
- Continue-watching affordance (jump to the next unwatched episode)

This is a **read path** over `series` / `series_episodes` (26.3),
enrichment (26.5), and existing **watch progress** (`play_state` /
`watch_history` from Epic 08/14). It adds **no new tables**.

## Acceptance criteria

- `GET /api/series` lists series across all libraries the user can
  access (filterable by `?library_id=`, sortable by name/recently-added/
  progress), each with poster, name, `season_count`, `episode_count`,
  and aggregate watch progress.
- `GET /api/series/{id}/episodes` returns episodes grouped by season and
  ordered by episode number (absolute order for absolute-numbered
  series), each with thumbnail, title, duration, and the user's
  per-episode watch state (unwatched / in-progress %, watched).
- `GET /api/series/{id}/missing` returns the gaps in each season's
  episode sequence (e.g. season 1 has E01,E02,E04 → missing E03), based
  on the contiguous-range expectation; gaps render as ghost cells.
- The series page surfaces **continue watching**: the next unwatched (or
  in-progress) episode in series order, with a one-click resume.
- Watch progress is read from the existing playback-state store; marking
  an episode watched/unwatched there is reflected here (no parallel
  progress store).
- Series the user has renamed or whose episodes they reorganised (26.3
  overrides) display the user's version.
- ACL: a cross-library series only shows episodes from libraries the
  user can access; an episode in a forbidden library is hidden (and
  excluded from counts/missing-detection for that user).
- The web Series browser is a new route (`/series`, `/series/:id`) using
  the existing design system; responsive grid; RTL-correct for Arabic
  series names.

## Test cases

- `test_list_series_across_libraries` — series from 2 libraries → both
  listed; `?library_id=` filters to one.
- `test_episode_grid_grouped_and_ordered` — multi-season series →
  episodes grouped by season, ordered within season.
- `test_absolute_numbering_order` — absolute-numbered series →
  single pseudo-season ordered by absolute number.
- `test_missing_episode_detection` — season with E01,E02,E04 → missing
  reports E03.
- `test_watch_progress_surfaced` — episode at 40% in `play_state` →
  grid shows 40% in-progress; continue-watching points to it.
- `test_continue_watching_next_unwatched` — E01 watched, E02 unwatched →
  resume targets E02.
- `test_user_override_displayed` — renamed series / reordered episode →
  user version shown.
- `test_acl_hides_forbidden_episodes` — an episode in a forbidden
  library is absent from list, grid, counts, and missing-detection.
- `test_rtl_render` (web) — Arabic series name renders RTL in grid and
  detail.

## Edge cases

- **Specials / season 0.** Rendered as a separate "Specials" row;
  excluded from missing-episode detection for numbered seasons.
- **Absolute vs. season numbering.** Missing-detection only runs where a
  contiguous expectation exists; for absolute-only series it reports gaps
  in the absolute sequence and notes the numbering mode.
- **Unknown episode count from the source.** Missing-detection is
  range-based over present episodes; it does not invent a total from
  enrichment unless an enriched season episode-count is available, in
  which case trailing-missing episodes (E05,E06 of a 6-ep season) are
  shown too.
- **Cross-library episode duplicates** (same episode in two libraries).
  Shown once with a "in N libraries" badge; counts don't double.
- **A series with one episode.** Still listed (it's a valid 1-episode
  series); missing-detection reports nothing.
- **Watch progress device sync.** Progress reflects the shared
  playback-state store; no Epic 26-specific progress is stored.
- **Large catalogues.** `/api/series` paginates; the grid virtualises.
