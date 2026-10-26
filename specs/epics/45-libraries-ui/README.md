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
