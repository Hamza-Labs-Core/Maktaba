# Story 57.01 — Age tiers on the home's content

> Epic 57 · Phase 1 · Depends on: — · Needed by: 57.03, 57.04, 57.10, 57.11, 57.12

## Why

Nothing on the home has a rating column. The ratings that exist are free text in four places
(Plex's `contentRating`, the guide's `title_snapshot.rating`, which no importer writes yet, a
plugin's `content_rating`, and an enrichment field nothing writes), and YouTube's age limit is
not kept at all. A filter needs one number per video, decided the same way every time, in Go
and in Swift.

## Description

### 1. The scale and the rating tables

Tiers 0 (All ages), 9, 13, 16, 18, and null (unrated), as README §6.1. One data file,
`shared/parental/ratings.json`, maps each known rating to a tier:

Each label has a nominal age. Content takes the smallest tier at or above it (rounded up); a
limit takes the largest tier at or below it (rounded down). Parental-guidance labels (PG, TV-PG)
count as 9, the App Store's 9+.

| System | Key | Label → nominal age → tier as content / as a limit |
|---|---|---|
| US MPA | `us-movie` | G 0 → 0/0; PG 9 → 9/9; PG-13 13 → 13/13; R 17 → 18/16; NC-17 18 → 18/18 |
| US TV | `us-tv` | TV-Y, TV-G 0 → 0/0; TV-Y7 7 → 9/0; TV-PG 9 → 9/9; TV-14 14 → 16/13; TV-MA 17 → 18/16 |
| UK BBFC | `gb` | U 0 → 0/0; PG 9 → 9/9; 12, 12A 12 → 13/9; 15 15 → 16/13; 18 18 → 18/18 |
| Saudi GCAM | `sa` | G 0 → 0/0; PG 9 → 9/9; PG12, R12 12 → 13/9; R15 15 → 16/13; R18 18 → 18/18 |
| Australia | `au` | G 0 → 0/0; PG 9 → 9/9; M, MA15+ 15 → 16/13; R18+ 18 → 18/18 |
| Germany FSK | `de` | 0 → 0/0; 6 → 9/0; 12 → 13/9; 16 → 16/16; 18 → 18/18 |
| Plain | `age` | `NN+` or `NN` → NN |
| Apple scale | `apple-movie`, `apple-tv` | limits only: the US anchors above (1000 → no limit) |

A limit of "nothing allowed" (Apple's 0, Don't Allow) is tier −1: only allow-listed items show.

Parsing: Plex writes `gb/15`, `de/12`, `TV-14`, `PG-13`, `Approved` and others. A `cc/label`
prefix picks the system; a bare label tries `us-movie`, then `us-tv`. An unknown string is
unrated, and is logged once per distinct string (`parental.rating_unknown`) so an admin can see
what to add.

`GET /api/parental/ratings` returns the scale and the tables, so the apps never ship their own
copy. MaktabaKit bundles the file only as the vectors' source for tests.

### 2. Where a video's tier comes from

README §6.1's order: admin on the video; the strictest admin tier on a containing series,
collection (manual or automatic), YouTube source or channel; the strictest metadata rating
(Plex, guide, YouTube `age_limit`, plugin, and `detected` from the transcript when its confidence
passes the line, 57.17); the library's default; unrated. The result goes in
`videos.age_tier` with `age_tier_source`.

- **Recompute** is a function `parental.Resolve(video)` called from: the admin routes below, the
  YouTube import (when `age_limit` is set; the import stage keeps the value and the API's
  `parental_recompute` job writes the tier, see README §2.4),
  the plugin `set-field` route for `content_rating`, collection membership changes, and series
  detection. A container's tier change enqueues a recompute for its members in batches of 1,000
  (the queue's `parental_recompute` job, idempotent).
- **Plugins never loosen.** A plugin's rating is a metadata source (step 3), so an admin's tier or
  a container's tier always wins, and among metadata sources the strictest wins. A plugin's
  rating (and the `detected` source of 57.17) can only tighten: when it is the video's only
  rating and the library has a default, the tier is the stricter of the two, so a plugin never
  brings a video under the default an admin set. Plex, guide and YouTube ratings keep their
  place over the default.
- **YouTube:** the import stage keeps yt-dlp's `age_limit` (0 or 18) in `youtube_videos.age_limit`.
  Existing YouTube videos get it on their next metadata refresh. Until then they fall to the
  library default.

### 3. Admin routes

`PUT /api/videos/{id}/age-tier`, `PUT /api/libraries/{id}/age-tier`,
`PUT /api/series/{id}/age-tier`, `PUT /api/collections/{id}/age-tier`,
`PUT /api/channels/{id}/age-tier`, `PUT /api/libraries/{id}/youtube/sources/{sid}/age-tier`,
each `{tier: 0|9|13|16|18|null}`; admin only (403 for viewers and guardians who are not admins).
Video and container answers return the new tier, its source, and how many videos a container
change will recompute.

### 4. Migration (slot 0220, planned; see README §6.5)

`0220_age_tiers.sql` and `.sqlite.sql`. Existing rows start with null tiers; a one-off
`parental_recompute` over every video runs after the migration (in the background, by library,
newest first) so Plex, guide and plugin ratings that already exist fill in.

## Acceptance criteria

- **Given** a kept `plex_items` row with `contentRating` `gb/15`, **when** the recompute runs, **then** the
  video's tier is 16 with source `plex`.
- **Given** that video in a collection an admin set to 18, **then** its tier is 18 with source
  `admin_container`; **given** the admin then sets the video itself to 13, **then** it is 13 with
  source `admin_video`.
- **Given** a plugin sets `content_rating` `G` on a video whose Plex rating is `R`, **then** the
  tier stays 18.
- **Given** a YouTube video with `age_limit` 18, **then** its tier is 18 with source `youtube`.
- **Given** a library default of 0 and a video with no rating, **then** its tier is 0 with
  source `library_default`; with no default, null.
- **Given** the vector file, **then** Go and Swift map every vector to the same tier.
- **Given** a viewer or a guardian who is not an admin, **when** they call an age-tier route,
  **then** 403.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | `shared/parental/vectors.json` (every label of every system, content and limit) | parse + map | each expected tier |
| T02 | MaktabaKit XCTest | the same vector file | map | identical to T01 |
| T03 | Go unit | the precedence cases above | `Resolve` | tier and source per case |
| T04 | Go + Postgres | collection of 3,000 videos | set collection tier | recompute in 3 batches; all members updated |
| T05 | Go unit | plugin `G` vs Plex `R`; plugin `R18+` vs admin 9; plugin `G` alone vs library default 13 | `Resolve` | 18; 9; 13 |
| T06 | pytest | yt-dlp info with `age_limit: 18` | YouTube import | `youtube_videos.age_limit = 18` |
| T07 | Go unit | unknown label `XYZ` twice | parse | unrated; one log line |
| T08 | Go role test | no session, viewer, guardian, admin | each age-tier route | 401, 403, 403, 200 |
| T09 | migration test | up/down Postgres and SQLite | migrate | twins apply; MANIFEST row |

## Strings

Tier labels are shown by the apps (iOS catalogs, `MaktabaKit/Localizable.xcstrings`):

| Key | English | Arabic |
|---|---|---|
| `parental.tier.all` | All ages | لكل الأعمار |
| `parental.tier.9` | 9+ | ‎+9 |
| `parental.tier.13` | 13+ | ‎+13 |
| `parental.tier.16` | 16+ | ‎+16 |
| `parental.tier.18` | 18+ | ‎+18 |
| `parental.tier.unrated` | Unrated | بلا تصنيف |
| `parental.tier.none` | Nothing unless allowed | لا شيء إلا ما يُسمح به |

The OpenAPI summaries carry Arabic in `shared/api/openapi.ar.json` (57.13).

## Edge cases

- **A video in two collections** with tiers 9 and 16: 16 (strictest container).
- **An admin clears a video's tier** (`null`): it falls back down the order, not to unrated.
- **Plex ratings**: `plex_items` is no longer refreshed (README §2.4), so a kept `contentRating`
  never changes. The recompute reads it as it is.
- **SQLite** (the single-user desktop build): the same columns; recompute runs in-process.
- **Guide ratings (a known gap)**: the `guide` source reads
  `channel_programs.title_snapshot.rating`, and slot 0220's trigger and partial index watch it,
  but nothing in the pipeline, the first-party plugins or the API writes that key: the programme
  importers keep no rating in `title_snapshot`. Until one does, no video takes a tier from the
  guide. The source, the trigger and the index stay in place for it and cost nothing meanwhile
  (the index holds only programmes that carry a rating). Writing the rating where programmes are
  imported is not part of this story.

## Build plan

| File | Change |
|---|---|
| `shared/parental/ratings.json`, `shared/parental/vectors.json` (new) | tables and vectors |
| `api/internal/parental/ratings.go`, `resolve.go` (new), tests | parse, map, resolve |
| `api/internal/handlers/parental/tiers.go` (new) | admin routes |
| `api/internal/plugins/routes.go` | `content_rating` → recompute |
| the YouTube import stage, and its role's grants (`tools/plugins/roles.py`) | keep `age_limit`; enqueue `parental_recompute` |
| `shared/db/migrations/0220_age_tiers{,.sqlite}.sql`, `MANIFEST.md` | columns |
| `apps/apple/MaktabaKit/Tests/MaktabaKitTests/RatingVectorsTests.swift` (new) | T02 |
