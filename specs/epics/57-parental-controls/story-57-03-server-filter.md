# Story 57.03 — One content filter on every surface

> Epic 57 · Phase 1 · Depends on: 57.01, 57.02 · Needed by: 57.04, 57.05, 57.10, 57.11, 57.13
> **Status:** built (checked against the code 2026-11-12). Open: a restricted account's Home measured over the
> 60 ms bound of T12 (see As built).

## Why

The home has no central access check: about 15 packages each scope by library their own way.
A parental filter added to each by hand would be missed in one of them, and a child needs only one
gap. The filter must be one function, used by every package, with a test that fails when a new
content route skips it.

## Description

### 1. `api/internal/parental`

- `Limit(ctx, p) Effective`: from the principal's policy (57.02, cached, invalidated by
  `pg_notify`) and the access token's `ac` claim (57.04), the strictest tier, the unrated rule,
  `allow_only`, the rules, and a `version`. An unrestricted principal with no signal gets
  `Effective{}` and every function below short-circuits (no extra SQL).
- `SQL(e, alias) (clause string, args []any)`: one predicate on `videos` (`age_tier`, the rule
  subjects joined through `collection_items`, `series_episodes`, `youtube_videos`), placed next to
  each package's library scope.
- `Allowed(ctx, e, kind, id) bool` for single objects.
- `NotFound(w)` writes exactly what the package's own missing-ID answer writes, so a blocked item
  and a missing one are the same bytes.

### 2. Every surface

| Surface | Change |
|---|---|
| `browse` (Home, hub, categories, On Deck, Continue Watching, favourites, Recently Added, pins) | `SQL` in each query; the row cache stores each row's items with `age_tier` and filters on read (its per-library key stays right) |
| `videos` (`/api/videos`, `/api/me/videos`, `/{id}` and `/poster`, `/trickplay`, `/segments`, `/subtitles`, `/chapters`, `/context`, `/facets`) | `SQL` on lists and facets (counts exclude hidden items); `Allowed` + `NotFound` on by-ID routes |
| `search` (`/api/search`, `/results/{sid}`, `/passages`, `/suggest`, `/saved`) | `SQL` in the ranking query and passages; suggestions come from allowed titles only; saved searches re-run filtered |
| `search/youtube`, `import-youtube` | 403 `restricted` for a restricted principal or a sign-in with a signal |
| `recommendations` (`user_recs`) | filtered on read; hidden items do not count toward a row's size (it fills from the next) |
| `collections`, `series` | lists, items, counts and posters filtered; a collection or series that is blocked, or whose items are all hidden, answers `NotFound` |
| `browse/mine` playlists, `playqueue` | items filtered on every window read (`checkVisible` gains `Allowed`); a queue's source must be allowed at creation |
| `watch` history, `/api/me/history` | the child's own history shows hidden items as "Not available" without title or poster |
| `streaming` (`POST /api/stream/sessions`) | `Allowed` before a session; a restricted sign-in's signed URLs get a TTL ≤ 15 min (instead of the 24 h cap). There is no renewal route: opening the session again re-checks, and the progress post (`POST /api/stream/sessions/{id}/progress`) answers the 404 of a missing session once the video is hidden, so the session's lease runs out |
| `reports.thumbVisible` (`/api/thumbnails/{hash}`) | joins the video's tier |
| `plugins/panels` (`/api/panels`, `/panels/image`) | the subject must be allowed, and the plugin must be allow-listed for a restricted child (panels may name other titles) |
| `ws` | events about a video are sent only if `Allowed`; queue and job events are admin-only already |
| `graphql` | inherits through the REST bridge |
| channels, guide, live streams | story 57.10 |
| `devices/downloads` | a download flag for a hidden video answers `NotFound` |
| personal access tokens | creating one as a restricted user: 403 `restricted` |

### 3. The surface registry

`api/internal/parental/surfaces.go` (`parental.Surfaces`) names every route of the API, not
only the ones that return content, and says what each is to a restricted account: `open` (no
content, or the route guards itself), `filtered` (it applies the filter itself), `by_id` (the
gate checks the object named in the path before the handler runs), `refuse` (403 `restricted`)
or `closed` (the answer of a route that does not exist). Only `refuse` and `closed` may be given
by prefix. A route that answers content carries a probe: how to ask it for a fixture video,
series, collection, library or channel. The router walk
(`api/internal/router/parental_walk_test.go`) fails on any mounted route no entry names, and
the bypass rehearsal (57.13) runs every probe.

## Acceptance criteria

- **Given** a home with a 9+ child and fixtures `v_ok` (0), `v_hi` (16) and `v_unr` (unrated, policy
  `hide`), **when** the child calls every listed route, **then** `v_hi` and `v_unr` never appear,
  and by-ID routes for them answer exactly as for a random UUID.
- **Given** the same with an allow rule on `v_hi`, **then** `v_hi` appears everywhere `v_ok` does.
- **Given** a playing stream of `v_ok`, **when** a guardian blocks `v_ok`, **then** the next
  progress post answers 404, the stream ends when its lease runs out, opening the session again
  is refused, and the Player shows the blocked page.
- **Given** an unrestricted user with no signal, **then** queries are unchanged (the plan has no
  extra join; benchmark within 2% of before).
- **Given** a facet count on a library, **then** it counts only what the child can see.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | effective limits | `SQL` and `Allowed` | match the rule table of 57.02 |
| T02 | Go + Postgres | fixtures | each by-ID route for `v_hi` vs a random id | identical status, headers (less `Date`, request id) and body |
| T03 | Go + Postgres | fixtures | poster, trickplay, subtitles, chapters, context, thumbnail by hash | 404 for hidden |
| T04 | Go + Postgres | `v_hi` with a transcript word | search, passages, suggest | absent |
| T05 | Go + Postgres | warm row cache from an admin's request | child's Home | hidden items absent; admin's still present |
| T06 | Go + Postgres | queue made before a block | read window | blocked item skipped |
| T07 | Go unit | ws subscription | a `v_hi` event | not delivered to the child |
| T08 | Go + streaming | restricted stream | open again, and post progress, after a block | refused (404); TTL ≤ 15 min |
| T09 | Go role test | restricted child | PAT create, YouTube search, import | 403 `restricted` |
| T10 | Go unit | panel on `v_ok` by a plugin not allowed | `/api/panels` | none |
| T11 | Go + Postgres | the web's own routes as the child (cookie session) | list, by ID | filtered the same |
| T12 | Go benchmark (`p57_filter_bench_test.go`) | 150,000 videos; unrestricted and restricted | `/api/videos` page, Home | unrestricted within 2%; restricted p95 ≤ 60 ms. The benchmark reports the p95 and does not assert it |

## Strings

None in the API (codes only). The Player's blocked page strings are in 57.05.

## Edge cases

- **A series with some episodes hidden**: the series shows with only allowed episodes; Continue
  skips hidden ones; episode numbers keep their gaps.
- **A collection whose poster is a hidden video's**: the poster comes from the first allowed item.
- **Counts that would leak** ("12 videos" when 3 are visible): every count is computed under the
  filter.
- **SQLite**: the same predicate. The router tests of this story (`p57_*_pg_test.go`) run on
  Postgres.

## Build plan

| File | Change |
|---|---|
| `api/internal/parental/filter.go`, `surfaces.go` (new), tests | the package |
| `api/internal/handlers/{browse,videos,search,recommendations,collections,series,playqueue,watch,streaming,reports,ws,devices}/` | call it |
| `api/internal/plugins/panels.go` | subject + plugin allow |
| `api/internal/auth/signedurl/` | restricted TTL |
| `api/internal/auth/pat/` | refuse restricted |
| `api/internal/router/p57_bypass_pg_test.go` (new) | the rehearsal's probes (57.13) |

## As built

- The filter also covers what the table of §2 does not name one by one: search (titles,
  transcripts, the meaning search's passages, kept searches, recent searches), the live feeds
  (SSE and the GraphQL subscription ask a guard for every event and end when their library or
  video is hidden), tags and a library's numbers (counted over what the account may see), and
  plugin panels.
- **The Home row cache is not used for a restricted account.** Its rows are read from the
  database under the filter; the shared cache keeps serving accounts with no limit (T05).
- **Open: the 60 ms bound.** With the row cache bypassed, a restricted account's Home measured
  over the 60 ms p95 bound of T12. The benchmark prints the figure and asserts nothing, since a
  shared runner's tail measures the machine. The bound stays the target.
- A single item the filter refuses is one `blocked` event in the child's activity (57.08), one
  per subject per day.
