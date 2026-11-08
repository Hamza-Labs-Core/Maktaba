# Story 57.03 — One content filter on every surface

> Epic 57 · Phase 1 · Depends on: 57.01, 57.02 · Needed by: 57.04, 57.05, 57.10, 57.11, 57.13

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
| `streaming` (`POST /api/stream/sessions`) | `Allowed` before a session; a restricted sign-in's signed URLs get a TTL ≤ 15 min (instead of the 24 h cap); renewal re-checks |
| `reports.thumbVisible` (`/api/thumbnails/{hash}`) | joins the video's tier |
| `plugins/panels` (`/api/panels`, `/panels/image`) | the subject must be allowed, and the plugin must be allow-listed for a restricted child (panels may name other titles) |
| `ws` | events about a video are sent only if `Allowed`; queue and job events are admin-only already |
| `graphql` | inherits through the REST bridge |
| channels, guide, live streams | story 57.10 |
| `devices/downloads` | a download flag for a hidden video answers `NotFound` |
| personal access tokens | creating one as a restricted user: 403 `restricted` |

### 3. The surface registry

`api/internal/parental/surfaces.go` lists every route that returns content, each with a probe:
how to ask it for a fixture video, series, collection or channel. 57.13's router walk fails on a
mounted route that returns content and is not listed (a route is "content" if its handler package
is one of the table's, or it is tagged `content` where mounted), and the bypass rehearsal runs
every probe.

## Acceptance criteria

- **Given** a home with a 9+ child and fixtures `v_ok` (0), `v_hi` (16) and `v_unr` (unrated, policy
  `hide`), **when** the child calls every listed route, **then** `v_hi` and `v_unr` never appear,
  and by-ID routes for them answer exactly as for a random UUID.
- **Given** the same with an allow rule on `v_hi`, **then** `v_hi` appears everywhere `v_ok` does.
- **Given** a playing stream of `v_ok`, **when** a guardian blocks `v_ok`, **then** the next URL
  renewal (≤ 15 min) is refused and the Player shows the blocked page.
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
| T08 | Go + streaming | restricted stream | renew after block | refused; TTL ≤ 15 min |
| T09 | Go role test | restricted child | PAT create, YouTube search, import | 403 `restricted` |
| T10 | Go unit | panel on `v_ok` by a plugin not allowed | `/api/panels` | none |
| T11 | Go + Postgres | the web's own routes as the child (cookie session) | list, by ID | filtered the same |
| T12 | Go benchmark | 150,000 videos; unrestricted and restricted | `/api/videos` page, Home | unrestricted within 2%; restricted p95 ≤ 60 ms |

## Strings

None in the API (codes only). The Player's blocked page strings are in 57.05.

## Edge cases

- **A series with some episodes hidden**: the series shows with only allowed episodes; Continue
  skips hidden ones; episode numbers keep their gaps.
- **A collection whose poster is a hidden video's**: the poster comes from the first allowed item.
- **Counts that would leak** ("12 videos" when 3 are visible): every count is computed under the
  filter.
- **SQLite**: the same predicate; T02–T06 run on both stores.

## Build plan

| File | Change |
|---|---|
| `api/internal/parental/filter.go`, `surfaces.go` (new), tests | the package |
| `api/internal/handlers/{browse,videos,search,recommendations,collections,series,playqueue,watch,streaming,reports,ws,devices}/` | call it |
| `api/internal/plugins/panels.go` | subject + plugin allow |
| `api/internal/auth/signedurl/` | restricted TTL |
| `api/internal/auth/pat/` | refuse restricted |
| `api/internal/parental/bypass_pg_test.go` (new) | the rehearsal's probes (57.13) |
