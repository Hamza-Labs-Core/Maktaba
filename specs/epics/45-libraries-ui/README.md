# Epic 45: Libraries: manage them, browse them, and the server's storage settings

## Why

Maktaba.app is both the server and a player: its window shows the web app. Today that web app can
browse one flat list of videos (`/library`) and start a scan, and nothing else:

- **No page manages libraries.** The API has `POST/PATCH/DELETE /api/libraries`, but the only way to
  add a library or change its folders is `curl`. The home has 66 libraries, one per YouTube channel,
  all under `/Volumes/Originals/Backup/Youtube/`.
- **Three folders belong to two libraries each**, made by the move from the dev setup on 2026-10-17:
  `alhewenytube` ("alhewenytube", 1 video, and "الشيخ أبو إسحاق الحويني", 6,191), "Dr. Othman
  Alkamees" (1 and 47,273) and "د. أيمن سويد Dr. Ayman Swaid" (1 and 1,006). Nothing flags them and
  nothing can fix them without SQL.
- **The storage and discovery settings live only in `server.toml`**: `[media] path_map` (the database
  stores the NAS's paths, `/Volumes/Originals/...`, which this Mac reaches as
  `/Users/<me>/nas-nfs/...`), `[media] roots`, `[server] mdns`, the listen addresses and the public
  URLs. Changing one means editing a file the server owns and restarting it from a terminal.
- **The API checks a new library's folder without the path map**: on this Mac a folder entered as
  `/Volumes/Originals/...` (the form the database uses) is refused as `not-found`.
- **Browsing is one grid.** No home page, no per-library page, no "continue watching", no way to keep
  the libraries one watches at hand.

Plex's web app was the reference (Home with Continue Watching / On Deck / Recently Added rows,
library sections with Recommended and Library tabs, pinned libraries in the sidebar, sort and filter,
the Add Library wizard with its folder browser, Manage Library → Scan / Empty Trash / Edit / Delete).
We take what fits a library of lectures and channels, not a clone.

## Goals

1. An admin can see every library with its folders, counts, last scan and problems; add one (with a
   folder picker that browses the server's disk), rename it, change its folders, scan it and follow the
   scan, empty its trash, merge it into another and remove it, each with a confirmation that says what
   happens to the videos and their data.
2. Duplicate and nested folders are flagged on the page, with a one-click fix: merge the duplicate
   library into the one that holds the videos.
3. An admin can see and change the storage and discovery settings (`path_map`, `roots`, `mdns`, the
   listen addresses, the public URLs) in Settings, safely: validated before they are saved, saved by
   the server that owns the file, and applied by a restart the admin starts and watches. The page shows
   which library folders this machine reaches through the path map.
4. Every user gets a Plex-like way to watch: a Home page across their libraries, a Libraries page of
   cards, a page per library with rows (Continue Watching, Recently Added, Recently Watched, Most
   Watched, Series, By year) and a full grid with sort and filters, and pinned libraries in the main
   navigation, in the order they choose, stored on the server.
5. Every role rule is the API's, with a role test: management and server settings are admin-only;
   browsing shows only the libraries the user may see (`principal.AccessAllLibraries` /
   `principal.Libraries`), and a library they may not see answers 404, as `GET /api/libraries/{id}`.
6. English and Arabic, RTL-safe, with help pages.

## Non-goals

- Library *types* (movies, shows, music) and metadata agents: every Maktaba library is videos.
- Deleting files from disk from the web. `DELETE /api/libraries/{id}?purge=true` exists and deletes the
  library's folders; the web never offers it. Removing a library removes Maktaba's records only.
- Changing the database URL, ports of the internal roles, `[postgres]`, `[transcription]` or `[cloud]`
  from the web (other pages own the last two; the rest stay in `server.toml`).
- Restarting a single role with a new environment: every setting here applies at a server restart.
- Smart collections UI (the API has them; nothing in the home uses them yet: 0 collections).

## Design

### Path resolution (API)

One resolver, `api/internal/libpaths`, used wherever the API touches a library folder:

- `ToLocal(dbPath)`: canonical share paths (`smb://…`, story 31.3, `sharepath`) first, then the
  `[media] path_map` the server hands every role as `MAKTABA_PATH_MAP` (`agent.ParsePathMap`, `;`
  separated `db=local` pairs or a JSON object), else unchanged.
- `ToDB(localPath)`: the reverse, for the folder picker (the user browses this machine's disk; the
  library stores the database's form).
- `OSPathChecker.Check` checks `ToLocal(root)`. That fixes the `not-found` above.

### Management API (admin; every route refuses non-admins with 403 before any query)

| Route | What |
|---|---|
| `GET /api/libraries/manage` | Every library: `{id, name, roots: [{path, local_path, reachable, problem}], settings, created_at, updated_at, counts: {total, missing, failed, ready, processing}, last_scan: {job_id, state, created_at, finished_at, error} \| null, scanning: bool, conflicts: [{root, other_root, library_id, library_name, kind: "same" \| "nested", other_count}]}`, plus `conflict_groups` (each set of libraries sharing a folder, with the suggested target: the one with the most videos). |
| `GET /api/libraries/folders?path=` | The folder picker. No `path`: `places` (the path map's local targets shown in database form, `[media]` roots, the existing libraries' parent folders, `/Volumes` on macOS, the home folder). With `path` (database or local form): `{path (database form), local_path, parent, entries: [{name, path}], truncated}`: directories only, no hidden ones, sorted by name, at most 2,000. A path that does not resolve to a readable directory is 404. |
| `GET /api/libraries/{id}/impact` | What removing (or merging) the library takes with it: `{videos, transcripts, playback_states, watch_sessions, series, collections, channel_blocks}` (counts; a table that does not exist counts 0). |
| `POST /api/libraries/{id}/merge` `{into, confirm}` | Merge a library into another (`confirm` = the merged library's name, else 412). In one transaction: each video moves to `into` unless `into` already holds the same file (`content_hash`); those duplicates are deleted (their data with them). Folders of the merged library that `into` does not already cover are added to `into`. The merged library is deleted. Answers `{moved, duplicates_removed, roots_added, into: Library}`; audit `library.merged`. |
| `POST /api/libraries/{id}/empty-trash` | Deletes the library's videos in state `missing` (the files are gone; Plex's Empty Trash). `{removed}`; audit `library.trash-emptied`. |
| existing `POST/PATCH/DELETE /api/libraries`, `POST …/scan` | Unchanged contracts; the overlap check now tells same from nested in its detail. |

The scan's progress is `GET /api/jobs/{id}` (exists); `manage` reports a running scan so the page can
follow it after a reload.

### Server settings (admin)

`server.toml` belongs to `maktaba-server`. The API (a role the server runs) never writes it. The
server's supervisor already serves a loopback admin port (`MAKTABA_SUPERVISOR_URL`, the per-boot
token in `MAKTABA_SUPERVISOR_TOKEN_FILE` as a bearer: `/update/`, `/switchover/`). It gains `/config/`:

| Supervisor route | What |
|---|---|
| `GET /config/settings` | `{file, saved: S, running: S, restart_needed, can_restart}` where `S = {media: {path_map: [{db, local}], roots: []}, server: {mdns: bool, listen, web_listen, stream_addr, public_url, web_public_url}}`. `running` is what this process started with; `saved` is the file now. |
| `PUT /config/settings` `S` (partial: only the keys sent change) | Validates (below), then load → change → `SaveTo` (atomic, 0600; every other key kept). 422 with `{errors: [{field, message}]}` on a bad value. Answers the `GET` document. |
| `POST /config/restart` | Under `maktaba-launcher` (`can_restart`): writes the launcher's restart request (`layout.RequestRestart`, the same file a self-update uses); serve drains and exits 75 and the launcher starts it again, reading the new file. Otherwise 409 ("restart the server yourself"). |

Validation: an address is `host:port` with a port 1–65535 and a host that is empty, an IP or
`localhost`; a changed address's port must be free on this machine; `listen` and `web_listen` must
differ; a public URL is empty or `http(s)://host[:port]` with no path, query or credentials; a
path-map pair needs both sides absolute and its local side an existing directory; a root must be an
absolute existing directory (through the path map). `mdns` is written `"on"`/`"off"`.

The API: `GET /api/server/settings` (the document plus `reachability`: each library folder, its local
path under the **saved** map, reachable or why not), `PUT /api/server/settings`, `POST
/api/server/restart`; all `RequireAdmin`, audited (`category='server'`). Without a supervisor (compose,
a lone `--role api`): `{available: false}` and 409 on writes.

The web's Settings → Storage & discovery shows the saved values, a "restart needed" banner while
`saved ≠ running`, and a Restart button that asks first, then waits for `/api/health` to answer again
and reloads. Changing an address warns that devices and the Mac app may need the new one.

### Browsing API (every signed-in user; only libraries they may see)

Migration **0159** (Postgres and SQLite):

```sql
CREATE TABLE user_library_pins (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  library_id UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  position   INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, library_id)
);
CREATE INDEX videos_created_at_idx ON videos (created_at DESC, id DESC);
CREATE INDEX videos_library_created_idx ON videos (library_id, created_at DESC);
CREATE INDEX videos_library_mtime_idx ON videos (library_id, mtime DESC);
```

A *video card* is `{id, title, filename, library_id, duration_sec, state, created_at, published_at
(mtime), position_sec?, completed?, last_watched_at?, plays?}`; its picture is
`/api/videos/{id}/poster`. Hidden states (`missing`, `superseded`, `deleted_at` set) are left out of
every row.

| Route | What |
|---|---|
| `GET /api/libraries/summary` | One card per visible library: `{id, name, videos, duration_sec, poster_video_ids (up to 4 recent ones), latest_added_at, pinned, pin_position}`. |
| `GET /api/me/pinned-libraries` / `PUT` `{library_ids: [...]}` | The user's pins in order. `PUT` replaces them (the order is the array's); a library the user may not see is 404 (the same as one that does not exist); at most 20. |
| `GET /api/home` | `{continue_watching, recently_added, recently_watched, pinned: [{library: {id, name}, recently_added, continue_watching}]}`, 20 cards per row. |
| `GET /api/libraries/{id}/hub` | `{library: {id, name, videos}, rows: {continue_watching, recently_added, recently_watched, most_watched, series: [{id, name, episodes}], years: [{year, videos}]}}`; 404 for a library the user may not see. |
| `GET /api/videos` (extended) | Adds `sort=published_at\|duration_sec\|plays` (besides `updated_at\|created_at\|title`), `order=asc\|desc`, and the filters `year=YYYY`, `watched=unwatched\|in_progress\|watched` (the caller's own), `series=<id>`. The cursor carries the sort and order. |

Definitions. *Continue watching*: the user's `playback_state` not completed, position over 30 s and
under 95 % of the duration, newest first. *Recently watched*: the user's `playback_state` and
`watch_sessions`, newest first, one card per video. *Recently added*: `created_at` then `mtime`,
newest first. *Most watched*: plays (`watch_sessions` rows, everyone's, counted, never listed per
user) in the last 90 days. *By year*: the year of `mtime` (yt-dlp sets a download's mtime to the
upload date). Epic 44's server-side play recording writes `watch_sessions`; these rows read it as it is.

### Web

| Route | Page |
|---|---|
| `/` | **Home**: Continue Watching, Recently Added, Recently Watched, Live TV on now (`WhatsOnNow`), then one row per pinned library. Empty rows are hidden; a first-run home says how to start. |
| `/library` | **Libraries**: a card per library (poster mosaic, name, video count, pin toggle); "All videos" link. |
| `/library/:id` | **Library page**: *Overview* tab (the hub's rows, horizontally scrolling, RTL-aware) and *Browse* tab (`?tab=browse`: the grid with sort, order, year, watched and language filters in the URL, grid/list toggle, Load more); a Pin button; admins see Scan and Manage. |
| `/videos` | **All videos**: the same grid over every visible library. |
| `/admin/libraries` | **Manage libraries** (admin): the list, the duplicate-folders panel with Merge, and dialogs: Add library (name, folders via the picker), Edit (name, folders, advanced settings), Scan with progress, Empty trash, Merge, Remove (with the impact counts). |
| `/settings/storage` | **Storage & discovery** (admin): path map pairs, media roots, LAN discovery, addresses, public URLs, reachability table, restart. |

Navigation: Home, Libraries, Series, Guide, Search; then a **Pinned** group with the user's pinned
libraries in their order (reordered with up/down buttons on the Libraries page: keyboard-accessible);
the admin group gains "Libraries" (`/admin/libraries`). The phone tab bar: Home, Libraries, Guide,
Search, More.

RTL: rows scroll with `scroll-padding-inline` and logical properties; the row's scroll buttons swap
direction under `dir="rtl"`; titles are bidi-isolated (`<bdi>`), as in the grid today.

Help: `library.md` grows Home, Libraries, the library page and pinning; new topics
`library-management` (adding, folders, duplicates, merge, empty trash, removing) and `storage`
(path map, roots, discovery, addresses, restart), in `en` and `ar`; `PAGE_HELP` maps the new routes.

## Stories

- **45.1** Path resolver; the folder check through the path map. Role tests unchanged.
- **45.2** `GET /api/libraries/manage`, conflicts, `GET /api/libraries/folders`, `impact`, `merge`,
  `empty-trash`; admin role tests for each.
- **45.3** Supervisor `/config/` (settings, validation, restart) and the API's `/api/server/*`
  proxy; admin role tests; config round-trip tests (unknown keys kept).
- **45.4** Migration 0159; `summary`, pins, `home`, `hub`, the `/api/videos` sorts and filters;
  library-ACL role tests (a viewer never sees another library's videos in any row or count).
- **45.5** Web: Manage libraries page and dialogs; Storage & discovery settings.
- **45.6** Web: Home, Libraries, library page, All videos, pinned navigation.
- **45.7** Help (en/ar), i18n, RTL check, browser verification on a branch stack (never the live
  database), and fixing the home's three duplicate libraries through the new Merge, by the owner.

## Acceptance

- The three duplicate folders show on Manage libraries with "Merge into <the large library>"; merging
  on a copy of the database leaves one library per folder with every video and its data.
- A viewer with one library sees only that library's cards, rows, counts, pins and grid, and gets 404
  for another library's hub, pin or folder; every management and server-settings route answers 403 to
  a viewer and to an anonymous caller.
- Changing `path_map` in Settings writes `server.toml` with every other key unchanged, shows "restart
  needed", and the restart brings the server back with the new map; a bad value is refused with the
  field named.
- vitest for each new page; Go tests for each new route; the help coverage test passes.

## Phase 2: Plex and Jellyfin parity (review of 2026-10-27)

Phase 1 (stories 45.1 to 45.7, PR 345) gave Maktaba a Plex-like shell: Home, library cards with pins,
a library page with rows and a Browse grid, management and storage settings. The owner asked for more
of what Plex and Jellyfin do. This review compares their library experience with Maktaba as the code
stands (checked in the code, not from memory) and says what fits a library of lectures, lessons and
sheikhs.

### Review

Have = done; Partly = some of it exists; Missing = nothing yet. Fit: how much it matters for a lecture
library (long talks, numbered lessons, one channel per sheikh, Arabic and English, transcripts).

| Feature (Plex / Jellyfin) | Maktaba before Phase 2 | Where | Fit |
|---|---|---|---|
| Continue watching | Have | `GET /api/home`, hub `continue_watching` (playback_state, 30 s to 95 %) | High |
| On deck: the next lesson of each series one is in the middle of | Missing | Series detail has a "Continue" button for one series; no row across series | High: lessons are watched in order |
| Up next at the end of a video, autoplay | Missing | The player stops at the end | High |
| Recently added | Have | Home and hub rows | High |
| Recently added grouped by series | Missing | A row of 20 lessons of one series floods it | Medium |
| Recently watched, watch history | Have | Rows; `/account/history` (`/api/me/history`) | High |
| Most watched | Have | Hub row (90 days) | Medium |
| Mark watched / unwatched, one video | Missing | No route; only the player writes `playback_state` | High |
| Mark watched / unwatched, many at once | Missing | | High: a sheikh's old lessons heard elsewhere |
| Watched and progress badges on the grid | Partly | Rows show progress; the Browse grid shows nothing | High |
| Collections | Partly | `/api/collections` (per library, smart ones too); no web page (a Phase 1 non-goal) | Medium |
| Playlists (per user, ordered, across libraries) | Missing | | High: "my Ramadan list", a study plan |
| Favorites / per-user ratings | Missing | | Favorites High; star ratings Low |
| Filter by genre / tag | Missing in the grid | `tags`, `video_tags`, `GET /api/tags` exist | Medium |
| Filter by speaker / sheikh | Missing in the grid | `speakers` (diarization names, renamed by admins); a library is usually one sheikh | Medium |
| Filter by language | Partly | `?language=`; the chips list only the languages of the loaded page | High (Arabic / English) |
| Filter by duration, resolution, added date | Missing | `duration_sec`, `media_info.width/height` exist | Low to Medium |
| Filter unwatched / in progress / watched | Have | `?watched=` | High |
| Sort options | Have | updated, added, published, title, duration, plays; asc / desc | High |
| A to Z jump bar | Missing | | Medium (47k videos in one library) |
| Poster / list views | Have | Grid / list toggle (kept per browser) | Medium |
| Search within a library | Partly | `GET /api/videos?q=` (title, description); no box on the page. Global search (`/search`, transcripts) exists | High |
| Rich detail page: chapters, transcript, people, related | Have | `/videos/:id` tabs (Chapters, Transcript), the context card (cast, related, more like this) | High |
| Detail page: watched, favorite, add to a list, series position, next / previous | Missing | | High |
| Series with seasons, episodes and progress | Have | `/series/:id` (Epic 26) | High |
| Hero / featured | Missing | | Low (no artwork beyond posters) |
| Trailers / hover previews | Missing | | Low (lectures have no trailers) |
| Scrub thumbnails (trickplay) | Missing | Posters and chapters exist; no sprite sheet | Medium |
| Resume points | Have | The player resumes from `playback_state` | High |
| Intros / chapters | Partly | Chapter ticks in the player and the Chapters tab; no skip intro | Chapters High; intros Low |
| Play all / Shuffle / Play from here | Missing | | High (a series, a playlist, a filtered grid) |
| Multi-select actions | Missing | | High (bulk watched, favorites, playlists) |
| Offline / download | Out of scope | The apps' (devices epic) | n/a |
| Sharing libraries with users | Have | Library ACLs (`/admin/library-acl`) | High |
| Managed users / profiles (switch with a PIN) | Partly | Users and roles; no profile switch | Low (each person signs in) |
| Parental controls | Missing | Library ACLs limit what a user sees | Low |

### Phase 2 design

Migration **0162** (Postgres and SQLite), additive:

```sql
CREATE TABLE user_favorites (
  user_id    UUID NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
  video_id   UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, video_id)
);
CREATE INDEX user_favorites_user_created_idx ON user_favorites (user_id, created_at DESC);
CREATE TABLE user_playlists (
  id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX user_playlists_user_idx ON user_playlists (user_id, updated_at DESC);
CREATE TABLE user_playlist_items (
  playlist_id UUID NOT NULL REFERENCES user_playlists(id) ON DELETE CASCADE,
  video_id    UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  position    INTEGER NOT NULL,
  added_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (playlist_id, video_id)
);
CREATE INDEX user_playlist_items_order_idx ON user_playlist_items (playlist_id, position);
CREATE INDEX videos_library_title_idx ON videos (library_id, (COALESCE(title, '')), id);
```

The three tables are a user's own and are left out of a machine's copy (`cluster_copy_excluded()`), as
the pins are: their routes run on the home.

Rules (every route below): the caller is signed in; a principal with no user row (the admin token) reads
empty lists and gets 403 on writes. A user reads and changes only their own marks, favorites and
playlists. A video in a library the caller may not see is 404, the same as one that does not exist; a
request naming several videos is refused whole (nothing written) if one of them is. Another user's
playlist is 404. A playlist keeps an item whose library its owner later loses, but never shows or
counts it.

| Route | What |
|---|---|
| `POST /api/me/watched` `{video_ids (1..500), watched}` | Marks watched (`playback_state`: completed, position 0, now) or unwatched (not completed, position 0; the time is kept, so it does not jump into Recently Watched). `{updated}` |
| `POST /api/me/favorites` `{video_ids (1..500), favorite}` | Adds or removes favorites. `{updated}` |
| `GET /api/me/videos/{id}` | The caller's state of one video: `{video_id, position_sec, completed, favorite, playlists: [{id, name}], series: {id, name} \| null}`. 404 when not visible. |
| `GET /api/me/playlists` | `{items: [{id, name, description, videos, duration_sec, poster_video_ids (up to 4), created_at, updated_at}]}`, the newest change first. At most 200 playlists a user. |
| `POST /api/me/playlists` `{name (1..200), description?, video_ids?}` | 201 and the playlist. |
| `GET /api/me/playlists/{id}` | `{playlist, items: [card]}` in order. At most 5,000 items a playlist. |
| `PATCH /api/me/playlists/{id}` `{name?, description?}`, `DELETE` | Rename, remove. |
| `POST /api/me/playlists/{id}/items` `{video_ids}` | Appends the ones not already in it. `{added}` |
| `DELETE /api/me/playlists/{id}/items/{video_id}` | Removes one. |
| `PUT /api/me/playlists/{id}/order` `{video_ids}` | The new order of the visible items (exactly them, each once; else 422); hidden items keep their place after them. |
| `GET /api/videos/facets?library=` | What the grid's filters offer, over the visible videos of the library (or of every visible library): `{languages: [{code, videos}], tags: [{id, name, videos}], speakers: [{name, videos}], letters: [{letter, videos}]}`. Speakers are the named ones (not a diarization label). Letters are the titles' first characters ("#" for any that is not a letter). 404 for a library the caller may not see. |
| `GET /api/videos` (extended) | Filters `tag=<id>`, `speaker=<name>`, `letter=<a letter or #>`, `sort=random` (one page, no cursor: Shuffle); each item carries the caller's `playback_state` and `favorite`. |
| `GET /api/me/videos` | The same list on the home, with `favorite=true` too. A machine serves `/api/videos` from its copy, which has no favorites: there `favorite=true` is 400 and the items carry no `favorite` mark. |
| `GET /api/home`, the hub (extended) | `on_deck`: for each series the caller is in the middle of, the next lesson after the last one finished, the series' newest activity first; on Home also `favorites`, the newest first. |

Web:

- **Browse grid** (All videos and a library's Browse tab): a search box (`q`), Tag, Speaker and
  Language filters from the facets, a Favorites toggle, an A to Z jump bar (sorted by title), watched
  ticks and progress bars on the cards, Play all and Shuffle, and Select: check cards, then Mark
  watched, Mark unwatched, Add to favorites, Remove from favorites, Add to playlist.
- **Video page**: Play, or Resume where you stopped; Mark watched / unwatched; Favorite; Add to
  playlist; for a lesson of a series, "Lesson n of m", the series' progress, Previous and Next.
- **Playlists** (`/playlists`, `/playlists/:id`): create, rename, remove; Play all, Shuffle, Play from
  here; move items up and down (keyboard), remove items.
- **Up next**: the player plays a queue (Play all, Shuffle, Play from here, a playlist) and, without
  one, the next lesson of the series. Near the end it shows "Up next" with a 10-second countdown, Play
  now and Cancel. The queue lives in the browser tab (`sessionStorage`).
- **Home**: On Deck and Favorites rows; **library page**: On Deck.
- English and Arabic, RTL-safe (the jump bar follows the script; arrows swap); help: `library`
  (filters, selecting, marks, favorites, up next) and a new `playlists` topic.

### Phase 2 stories

Built with this section:

- **45.8** Mark watched / unwatched, one and many; watched ticks and progress bars on the grid.
- **45.9** Favorites (per user): the route, the grid filter, the Home row, the button on the video page.
- **45.10** Playlists (per user): the routes, the Playlists pages, Add to playlist from the video page
  and from a selection.
- **45.11** Facets and the grid's filters: the search box, Tag, Speaker and Language.
- **45.12** The A to Z jump bar (`letter=`, the title index).
- **45.13** Play all, Shuffle, Play from here, the queue and the player's Up next.
- **45.14** The video page's actions and series position (previous, next, progress).
- **45.15** On Deck (Home and the library page).
- **45.16** Multi-select on the grid with the bulk actions.

Left for later, by priority:

- **45.17** Collections in the web (the API exists): a Collections row and page per library, curated
  by admins; smart collections as saved filters.
- **45.18** Recently Added grouped by series ("12 new lessons in Tafsir") on Home and the hub.
- **45.19** Scrub thumbnails (trickplay): a sprite sheet per video from the thumbnail stage, shown over
  the player's progress bar.
- **45.20** Duration and added-date filters (short / medium / long; last week / month / year) and a
  resolution filter from `media_info`.
- **45.21** A featured hero on Home (the newest series, or a lesson an admin picks).
- **45.22** Per-user star ratings, if favorites prove too coarse.
- **45.23** Profiles with a PIN in the Mac app's window and parental limits beyond library ACLs (Low:
  each person signs in).
- Not planned: trailers and hover previews, skip intro (lectures have neither), offline (the apps').
