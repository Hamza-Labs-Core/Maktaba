# Changelog

All notable changes to Maktaba are recorded here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The `[Unreleased]` section accrues entries between tagged releases.
The CI changelog gate (Story 22.5 AC-3) blocks user-visible PRs that
don't append a line here; docs-only PRs are exempt.

## [Unreleased]

### Added

- **Android Player: the shell for its sections (Epic 48.15).** Maktaba Player on Android gets
  Material's adaptive navigation (a bottom bar on a phone or a half-folded foldable on a table,
  a rail on an open foldable or small tablet, a drawer on a large tablet) for Home, Libraries,
  Live TV, Search and Profile, each keeping its own screens and scroll position. A section
  appears only once its screen is built, so for now the app opens on today's Libraries as
  before. CI now runs the Player's unit and UI tests on a fake home, with dark English and
  Arabic screenshots checked against references.
- **Subtitles show on Apple TV, AirPlay and Cast (story 48.4).** A video session's master
  playlist now lists each subtitle file beside the video as an HLS subtitle track (language,
  its own name, off by default). Each track is a one-file playlist over the existing subtitle
  route, signed like the rest of the session and timed to the picture, so AVPlayer, AirPlay
  and Cast can show it. hls.js in the web lists the tracks too. A video without subtitles gets
  the same playlist as before.
- **Epic 48 spec: the native Players reach the web and Plex.** `specs/epics/48-native-players/`
  lists what Epic 42 left out of Maktaba Player on iPhone, iPad, Mac and Android (the brand
  and a dark start, Home rails, Series, Search, Live TV, Profile, a full player with next
  episode, tracks, PiP, AirPlay and Cast), Live TV for the TV apps, one design-token source,
  four server gaps (a channel's stream must follow library access; signed stream URLs for
  AirPlay and Cast; subtitles as HLS renditions; small posters), 36 stories with testable
  checks, and the plan that runs them in parallel.
- **Maktaba Player (Apple) has its sections' shell (Epic 48, story 48.13).** It has Home,
  Libraries, Live TV, Search and Profile in a tab bar on iPhone that becomes a sidebar on
  iPad, Mac and an open iPhone Duo. A section shows only once its screen has shipped, so
  today's builds still open straight on the libraries. CI now runs the Player's UI tests,
  in English and Arabic, on throwaway iPhone and iPad simulators against a mock home, and
  keeps their screenshots.
- **The Apple apps speak Arabic.** Maktaba Remote (iPhone, iPad), Maktaba Player (iPhone,
  iPad, Mac) and Maktaba TV (Apple TV) ship in English and Arabic, right to left, on every
  screen: each app has a String Catalog, and MaktabaKit one of its own for the words it makes
  (the updates', processes' and queue's states, steps and reasons, the plan's sentence), in
  the web's Arabic (`ar.json`) wherever the web says the same thing. Counts take Arabic plurals;
  numbers and dates follow the locale; a value the home sends with no words is still shown as
  sent. CI's new *Apple apps* gate runs MaktabaKit's tests, builds the apps, and fails on a
  string without its Arabic or missing from its catalog (`tools/apple/check-string-catalogs.py`).
- **The Android apps speak Arabic.** Maktaba Remote (every screen: Needs attention, Queue,
  Updates, Libraries, Machines and its processes), Maktaba Player and the account menu now ship
  in English and Arabic, right to left, with numbers and dates in the device's language. The
  Remote says what the web admin says, in the web's own Arabic for each state, step and code; a
  value it has no words for is still shown as the home sent it. A unit test and lint now fail
  any Android or Android TV string that has no Arabic.
- **`docs/claude-trail.md`, the record a new Claude session reads first.** It holds the owner's
  standing rules, the decisions and why, failures and their fixes, the live state, the work in
  flight, what waits on the owner and the release history. `CLAUDE.md` tells every session to
  read it first and add to it before it ends.
- **The trail's PC sessions.** `docs/claude-trail.md` §9 now holds the Windows PC's own Claude
  sessions from 2026-10-14 to 2026-10-31: dizqueTV and the start of Maktaba TV, the node's
  database copy, the Windows and Linux apps, automatic updates, and how this PC's node is set up
  and kept running.
- **Maktaba Remote (iPhone, iPad) shows the home's running work.** *Home* links to two new
  read-only pages: *Updates* (the running release and what is next, in the web's words: what
  the planned release waits for, a stepping stone or a held release, a stale check; the
  releases still blocked; a TV held at an update with its failed checks; the run under way or
  the last one with its steps and every machine's state and error; each machine's release,
  last update and reach to the home's database) and *Processes* (each supervised process by
  role and TV slot, its state, restarts, last exit, crash loop and what it waits on, such as a
  port taken; the supervisor's recent events; or that the supervisor is not reachable). The
  *Queue* lists the jobs running now with how long each has run. An older or newer home's
  missing fields and unknown states are shown as they come instead of breaking the page.
- **The iPhone apps on TestFlight.** Every release tag builds Maktaba Player and Maktaba Remote
  for iPhone, signs them with Xcode's cloud signing (the App Store Connect API key) and uploads
  them to TestFlight (`testflight.yml`, `docs/testflight.md`). The apps' version is the tag's,
  and every upload gets a new build number.

- **Maktaba Remote for Android shows the home's running work, as the web admin does.** A new
  Updates tab: the version running, what the next update waits for, blocked releases (only the
  one still to come), a held TV and its failed checks, the run under way or the last one with
  its steps and each machine's state and error, and every machine's version. Machines lists the
  home's processes (TV slots by colour, crash loops, restarts, last exit, recent crashes; it
  says so when the supervisor is not reachable). Queue shows the jobs running now, the health
  strip, why each stage waits, its health, and a chosen stage's failures by cause. Read only.

- **The update rehearsal: every update path of the Mac app is proven in CI before a release.**
  A new workflow (`update-rehearsal`) updates a seeded home from the latest published release
  to the commit, the way Maktaba.app does (its bundle, launcher, state folder, bundled Postgres,
  and the app's install played by the test), and breaks it the ways it broke on 2026-10-28: a
  TV change that waits, rollbacks after additive and destructive migrations, a restore while
  machines log in and one killed part way, halted roles, no room, the owner's Mac's state, a
  joined machine updating before its home. Each scenario reports its downtime against a
  budget. It runs on pull requests that touch updates, on every push to main, nightly, and the
  release's tag guard refuses a commit it did not pass on (`docs/update-rehearsal.md`).

- **The update rehearsal covers a TV change staged on a slot that runs the old release.**
  Scenario 11 updates a home while its TV slots run, with a non-live slot (green) running the
  old streaming software across the restart. The update's TV change is then staged on it: the
  slot must start again on the new release and the change must reach verifying, never be held
  with `slot.health` / `release_mismatch` as change #58 was on 2026-10-31. The rehearsal now
  builds its streaming binary with TV staging, as the release does.

### Fixed

- **A Windows node installs the home's updates even when the Maktaba window is closed.** Until
  now the window app downloaded and installed each update. The server only tried to open it
  with a `maktaba://` link, so an update waited until a window could be opened. On 2026-10-31
  windows-pc's update to v0.3.44 sat waiting for eight hours, while Windows showed "Get an app
  to open this 'maktaba' link" every 90 seconds. Now the server agent that runs at sign-in
  (`Maktaba.exe --server-agent`) does it itself:
  - it downloads the release's installer from that release's own page;
  - it checks the installer with the app's key, and checks that the signature carries the
    ordered version and names that very installer;
  - at the home's go, it runs the installer.

  The server no longer sends the link, and the window app only shows the update.
- **A channel's stream follows library access (security, Epic 48 story 48.3).** The web
  port's `/stream/channel/{id}/…` checked only that someone was signed in, so a viewer
  limited to some libraries could watch another library's channel by its id. The gate now
  asks `GET /api/channels/{id}` with the viewer's credentials (the guide's rule): a channel
  the viewer may not read, or no such channel, is a 403. Admins skip the check; the verdict
  is cached per viewer and channel for 30 s, so segments cost no extra API call; the API not
  answering stays a 502. Video sessions and the admins' staging preview are unchanged.
- **A full node's copy of the home's database starts afresh even when a worker hangs.** On
  2026-10-28 a table's first copy was mid-way when the home's database restarted. Its
  connection never dropped, so the worker waited 15 minutes for rows that never came, and
  every fresh copy failed with "the subscription's workers did not stop within 30s". Now a
  worker still there 30 seconds after the copy was switched off is ended. Nothing restarts a
  worker of a copy that is off, so the fresh copy goes ahead.
- **The api and streaming integration tests no longer collide on a port.** Both started their
  server on the fixed ports 18081/19101. When CI ran the two packages at once, the streaming
  test could reach the api's server: "admin /readyz service = api, want streaming", which
  failed #450. Each test now takes free ports from the OS.

- **CI runs the Android Remote's unit tests.** The Android job ran the shared client's and
  core's tests but not the Remote's own (`:remote:testPhoneDebugUnitTest`), so the Remote's
  wording tests (`SayTest`) never ran in CI.

- **The supervisor keeps every TV slot on the release it is bound to.** It now records the
  binding each slot's process started with. Whenever a slot is bound (by any caller, not only the
  front door's start) or adopted after a restart, a wanted slot that runs another release is
  started again on its own. This extends the fix for change #58, which the TV held on 2026-10-31
  with `release_mismatch`.
- **The Processes card names each TV slot and says why one is not running.** The three TV slots
  (and several pipeline workers) were keyed and labelled by role alone, so they rendered as
  identical "tv-slot" cards. Each one is now named by its instance ("TV slot · blue"). A slot
  that isn't needed now says *Standing by*, not *Stopped*. A process held back by something
  (e.g. its port in use by another program) says what, in the app's words.

- **A Windows node mid-write of its node config is no longer read as never joined.** The
  streaming server's reader saw `node.json` missing during a write's swap, then looked for the
  write's lock after the write had ended, and took the two together for "not joined". It now
  looks at `node.json` once more before it decides, and reads it again if it is back. This was
  `TestNodeConfigPathWaitsOutAWrite` failing about once in 744 reads in CI.

- **CI builds on macOS 15.** GitHub retires its macOS 14 runners on 2026-11-02 and has started
  switching them off for hours at a time, which failed every darwin build and macOS installer job
  in about a second. The build, node packaging and release workflows now run on `macos-15`, as the
  Mac app and desktop release workflows already did.

- **A TV slot that was already running is started again when it is asked to run another release.**
  After the 0.3.44 update restarted the Mac, the green slot came back on the old TV software. The
  TV change then bound green to 0.3.44, but the supervisor only uses a new binding at a slot's next
  start, so green kept answering for its old release. The change's health check failed
  (`slot.health`: `release_mismatch`) and the TV stayed on v0.3.39. Now a start that brings another
  release restarts a running slot on it, and the same binding sent again changes nothing.

- **The Updates page no longer says "blocked" when the next update only waits.** It listed
  every release ever blocked, among them ones a newer release had long replaced (0.3.42,
  rolled back, once 0.3.43 was out), so a page whose update was only waiting for its quiet
  window read as blocked. A blocked release is now shown only while it is still the one to
  come. A release the TV could not move to has its own reason.

- **After a rollback that kept the newer schema, a release in between installs.** A rollback
  from a release whose migrations are additive keeps its schema for the release it goes back
  to; any release between the two was then refused as "older than the database", so the
  next fix could not be installed (update rehearsal 10a, once v0.3.43 shipped the kept
  schema). Every release from the one kept for up to the database's now starts on it.

- **The TV's blue and green slots start again on a Mac that transcribes in parallel.** The
  transcription workers took ports 9111 and up, the same ports as the TV slots' (9111–9113):
  on the owner's Mac two workers held 9111 and 9112 from 2026-10-28, and the blue and green
  slots crash-looped on them (over 1,300 restarts), which also blocked every update. The
  workers now use 9131–9146, and a test fails when any two processes' default ports match.
  A TV slot that cannot run no longer blocks an update from starting: TV never blocks one,
  and the update may be its fix.

- **A port another program holds is named, not crash-looped on.** Every port Maktaba listens
  on is now declared once, with its owner and what it is for (`shared/go/ports`), and every
  part takes its default from there; a test fails when two of them share a port, every port of
  a range included. At its start the server refuses addresses in `server.toml` that clash with
  one another, naming both. Before it starts a part, the supervisor checks that part's ports
  are free: one that is held leaves it not started, with `port_taken` in `/processes` saying
  which port and who holds it (the program and its pid where the system shows them, or which
  of Maktaba's own parts), and it tries again with the usual backoff.

- **On a Mac, the site comes back by itself after the server is killed.** A `kill -9` (or a
  crash) of the server left its bundled Postgres running, adopted by the system; every next
  start then failed on its `postmaster.pid` and the site stayed down. The server now stops a
  postmaster of its own cluster that nothing runs any more (fast shutdown, killed after 30 s)
  before it starts Postgres. Linux already did this through the launcher.

- **An update is no longer rolled back because its TV change waits, a rollback no longer puts
  the database back for additive migrations, and a restore that is needed is whole (the outage
  of 2026-10-28).** The update to 0.3.42 passed every check, but its TV change waited half an
  hour for an approval; the watch's bound ran out meanwhile (`watch_timeout`), the rollback put
  the whole database back for one added row, and that restore failed part way (a joined machine
  asking for its copy collided with the restore's grants: `tuple concurrently updated`), leaving
  the library down for a quarter of an hour. Now: the watch judges the home's own health (a TV
  slot starting or stopped by a swap is the TV step's; a live slot that crash-loops still rolls
  back), and a release healthy for its whole watch commits at once with its TV change
  **pending** (it goes on by itself; never `watch_timeout`, `tv_change_pending` or
  `tv_timeout`), or **held** with Retry when no change could be had at all. A rollback whose
  migrations are all rollback-safe keeps the schema and puts nothing back, for a previous
  release that says it runs on a kept schema (`shared/db/migrations/meta/schema-keep.json`; its
  api is told with `MAKTABA_SCHEMA_AHEAD_OK`); anything else that may break the previous
  release (a drop of any kind, a rename, a retype, a constraint or unique index added, a
  function's signature changed) puts the dump back. The new release puts it back itself before
  it switches back, so the previous one starts on a finished database: into a database of its
  own that nothing else reaches, the schema and the grants one at a time and the rows, vectors
  and indexes in parallel, checked against the dump (every table, index, key, trigger,
  function, grant and default privilege, and every table's rows: dumps now record them,
  `counts.json`), then renamed into place in one transaction with the database's own grants
  (the machines' CONNECT); a failure leaves the database as it was, retries with one worker,
  and says exactly what is missing. The roles halted by the database step start again by
  themselves once its record is put right (`migrated` or `restored`), a start gives the
  machines their CONNECT back when a restore dropped it, and a restart written by hand
  (`updates/restart`) is taken under the app's launcher too.

### Added

- **A library's Series and Collections tabs (Epic 45 story 45.24).** A library's page is now
  Overview | Browse | Series | Collections, each tab its own address (Back returns to the tab
  before). **Series**: every series of the library as a poster grid with its episodes and your
  progress, sorted by name, newest episode or recently watched. **Collections**: the collections
  Maktaba makes itself (topics "ملف", clips "مقاطع" and stand-alone "منفردات", the ones Plex shows,
  marked **Automatic** and read-only, each with its own page) next to the admins' curated ones,
  under a heading per kind with a filter and counts. The separate Collections button is gone and
  `/library/:id/collections` opens the tab. API: `GET /api/libraries/{id}/series`,
  `GET /api/libraries/{id}/collections`, `GET /api/libraries/{id}/collections/{topic|clip}/{id}`
  (404 for a library you may not see).
- **Plex-like watching, Phase 2 (Epic 45 stories 45.8 to 45.16).** Mark a video watched or
  unwatched, one at a time or many (**Select** on a library's grid); ticks and progress bars on
  the grid's cards; **Favorites** (a heart on the video page and the grid, a Favorites row on
  Home, a Favorites filter); **Playlists** (`/playlists`: make, rename, reorder, remove; add from a
  video or from a selection); the grid's **search box**, **Tag**, **Speaker** and **Language**
  filters and an **A to Z jump bar** (Arabic and Latin letters); **Play all**, **Shuffle** and
  **Play from here**, with **Up next** at the end of a video (the queue's next, or the series'
  next lesson, after a 10-second countdown); **On Deck** (the next lesson of each series you are
  in the middle of) on Home and a library's page; and on the video page Resume, the lesson's
  place in its series ("Lesson 4 of 40") with Previous and Next. Each user's marks, favorites
  and playlists are their own and stay within the libraries they may see. Slot 0162. API:
  `POST /api/me/watched`, `POST /api/me/favorites`, `GET /api/me/videos/{id}`,
  `/api/me/playlists...`, `GET /api/videos/facets`, `GET /api/me/videos` (the list on the home,
  `favorite=true`), and `GET /api/videos` gains `tag`, `speaker`, `letter` and `sort=random`.
- **Backup settings (Epic 36 story 36.16).** Where the library database's dumps go, the schedule
  (on/off, daily, weekly or every N days, the hour), how many scheduled and pre-update dumps stay,
  the pre-update dump on/off, and the room the dumps leave on their disk (5% / 2 GB / a fifth by
  default) are `[backups]` in server.toml, changed from **Settings → Storage & discovery →
  Backups of the library database** (which also lists and deletes dumps), setup's storage screen,
  or `maktaba-server backups show|set|delete`. Moving the folder asks whether to move the dumps.
  The time of the last scheduled dump is recorded apart, so moving or deleting dumps never makes
  an extra one. API: `GET/PUT /api/server/backups`, `DELETE /api/server/backups/dumps/{name}`
  (admin only, audited).
- **Maktaba's data on another disk (Epic 36 story 36.15).** First-time setup has a **Where
  Maktaba keeps its data** screen (the disks with their free space, the roomiest one offered, a
  warning when it is small) that sets `[server] data_dir` before anything is made. Later,
  `maktaba-server storage show` and `maktaba-server storage move <dir> [--dry-run]` (Maktaba
  stopped), or **Settings → Storage & discovery → Move to another disk** (admins; the server stops,
  copies, verifies and starts again), copy the database, backups, models and caches to another
  local disk, verify the copy (the database starts on it), point server.toml there and keep the
  old folders as `*.moved-<stamp>`. A cut-short move resumes. A start whose chosen disk is not
  connected waits and says so (`storage_waiting`) and never makes a new empty database in its
  place. API: `GET /api/server/storage`, `GET /api/server/storage/disks`,
  `POST /api/server/storage/check`, `POST /api/server/storage/move` (admin only, audited).
- **Libraries you can manage, browse and pin (Epic 45).** A **Home** page (`/`) across the
  libraries you may see: Continue Watching, Recently Added, Recently Watched, Live TV on now,
  and a row per pinned library. **Libraries** (`/library`) shows each library as a card with
  its posters and count; a library's page has an Overview (Continue Watching, Recently Added,
  Recently Watched, Most Watched, Series, By year) and a Browse grid that sorts by date added,
  date published, title, length or plays and filters by year, watched and language
  (`/api/videos` gains `sort=published_at|duration_sec|plays`, `order`, `year`, `watched`,
  `series`). Pin libraries to keep them in the sidebar, in your own order (stored per user on
  the server, slot 0159 `user_library_pins`). New API: `GET /api/home`,
  `GET /api/libraries/summary`, `GET /api/libraries/{id}/hub`,
  `GET/PUT /api/me/pinned-libraries`; every row and count is the caller's libraries only.
- **Manage libraries** (`/admin/libraries`, admins): every library with its folders (and
  whether this computer reaches them), counts, last scan and a live scan; add a library with a
  folder picker that browses the server's disk; rename, change folders, empty trash (the
  videos whose files are gone), merge into another library, and remove (files on disk are
  never touched), each confirmed with what it takes along. Folders that two libraries share
  are listed with a one-click merge into the library that holds the videos. New API:
  `GET /api/libraries/manage`, `GET /api/libraries/folders`, `GET /api/libraries/{id}/impact`,
  `POST /api/libraries/{id}/merge`, `POST /api/libraries/{id}/empty-trash`.
- **Settings → Storage & discovery** (admins): the path map, media folders, LAN discovery,
  the listen addresses and public URLs that lived only in `server.toml`, checked before they
  are saved, written by the server (every other key kept), and applied by a restart started
  from the page; a table shows each library folder and where this computer finds it. New API:
  `GET/PUT /api/server/settings`, `POST /api/server/restart` (slot 0160: the `server` audit
  category); the supervisor's loopback admin gains `/config/`.

- **Usage analytics, opt-in.** The web app asks once per device, "Help improve Maktaba",
  listing what is shared (the app's version, OS and language, the library's size as a
  range, how often each area is used, error reports without paths) and what never is (file
  names, transcripts, searches, who you are, your IP address). Nothing is sent before a yes.
  Settings → Privacy has the switch and "Forget my device", which deletes everything the
  device sent. The server stores the events (slot 0156) under the device's random pseudonym
  only: unknown kinds are refused, unknown fields dropped, library roots and home folders
  removed, 1,000 events a minute per IP, 90 days (Web Vitals 30). `[telemetry] enabled =
  "off"` in server.toml stores nothing.
- **A home has a name.** An admin names the home in Settings → Account (empty: the
  computer's name, which is the default). Every signed-in user's Apple app shows that name in
  its home list and title instead of the address that was typed. The API refuses the rename to
  anyone but an admin, and gives the name to every signed-in user in `GET /api/auth/me`
  (`home_name`).
- **Search results per video.** The Search page shows two lists, "In titles" and "In
  transcripts", one entry per video with no repeats. A transcript match shows the video's
  poster, title, library and duration, its true number of matching passages in the active
  transcript (no longer capped at 50 passages), its best passages with timestamps that open
  the player there, "Show all" for the rest, and an "Also in title" badge. Videos are ordered
  by their best match, then by how many passages match. Both lists have a List / Grid view
  (remembered), their own page numbers in the URL, and a video's matches page
  (`/search/video/{id}`) plays the video with a mark on the progress bar at every match,
  Previous / Next match, and every match in time order. A search's ordered results are kept
  under a `search_id` (slot 0148 `search_runs`) for 15 minutes after their last use, so pages
  never reorder; after that the search runs again with a notice. **Recent searches** lists
  each user's last 20 searches (live or expired, remove one or all). New API:
  `GET /api/search/results/{sid}`, `GET /api/search/passages`, `GET/DELETE /api/search/recent`;
  `POST /api/search` returns `transcript_videos` and the first pages (its flat `hits` stay one
  release, deprecated). Rules `rules.search` (passages per video, page sizes, how long results
  are kept, recent searches kept, most videos per list, the meaning search's top K).

- **Every full machine keeps a live copy of its home's database, and runs on it while the
  home is away** (Epic 37, `specs/epics/37-node-replica`). Nothing to set up: a machine that
  joins, or one joined before (at its first start on this version), makes its own Postgres
  (the home's major version: one it has, or the pinned PostgreSQL 16.15 build, fetched and
  checked once), migrates it and subscribes it, by logical replication over TLS that checks the
  home's certificate, to the publication the home keeps for it: every table but the secrets,
  other machines' rows and each database's own state. The home makes the machine's replication
  role, publication and slot itself and drops the slot of a machine that falls too far behind
  (`rules.cluster.replica_wal_keep_gb`, default 20 GB); the machine subscribes anew on its own
  then, and when the home's database is another one, its own Maktaba was updated, or the
  home's changes keep failing to apply, and it waits while the home's schema is ahead of its
  own. Every bundled Postgres runs with `wal_level=logical`. The machine reads the library from
  its copy while the copy is within `copy_reads_max_lag_seconds` (default 30) of the home. With
  the home away (refusing, or switched off and leaving connections hanging) it keeps working
  from that copy: pages read, the videos it can read play, its own TV channels play, a reload
  keeps people signed in, **signing in works** (checked on the copy, valid on that machine only;
  `offline_session_hours`, default 12, ending `offline_session_grace_minutes`, default 30, after
  the home is back), and **signing out works** (the session ends there at once; the home is told
  when it answers). Changes are refused until the home is back. A copy that had stopped
  following long before the outage does not answer (`offline_copy_max_lag_minutes`, default
  10). The copy's state shows on each machine's card on the Machines page and in the machine's
  own outage notice. Revoking a machine ends its subscription and wipes its copy. The node's
  pipeline stops claiming jobs while the home's database does not answer, stops the jobs in
  flight before their claim lease lapses (from the last beat the home answered), and resumes on
  its own; a job the home handed to another machine meanwhile is never ended by the first.
  Migration 0147.
- **The home can be switched to another machine** (Epic 37, "Switching the home"): "Make B the
  home" on the Machines tab, then nothing to do by hand. The home pauses the jobs and its
  database's changes, hands the machine what replication leaves out (the secrets and their key,
  the machines and their database roles with their passwords, the cluster key, the database CA,
  the Hugging Face token, an operator's JWT keys) sealed to that machine over the machines'
  channel, and the machine takes over without copying the database again: every other machine
  follows it from where its copy is, the old home becomes one of its machines, and a machine that
  was off follows when it is back (any machine of the cluster hands it a notice sealed to it).
  Two homes never write: once the promotion is asked the old home stays read-only until the new
  home says it is the home or called the switch off, also when its answer is lost (its app's
  database roles lose their rights to write its tables, which no session's own "read write"
  overrides, nor may they change its schema or give those rights back, and the machines' roles
  are kept out); that the promotion was asked is on disk
  before it leaves; one switch runs at a time; a switch is taken up after a restart of the old
  home, a thaw that failed is asked again, and a thaw ends the app's sessions opened while frozen
  (they kept a read-only default, so writes failed for up to half an hour after an undone switch);
  the promotion is one transaction (a failed one leaves
  the copy following the old home), and takes only a copy that has every table; the old home
  gives up its data only once it receives the new home's changes, and then keeps none of the
  home's keys (nor its database's certificate). An admin can call a switch off from the tab:
  before the promotion at once, after it only if the machine taking over has not promoted (or,
  when it does not answer, with its name typed: the old home then keeps asking it, and watches for
  its notice, and should it be the home after all, stops writing; a promotion called off is never
  finished, and its kit is removed, also when the undo did not reach it; the next switch begins
  past its term). Plex Live TV moves too (the tuner and its guide pointed at the
  new home, Plex's DVR, its recording schedules and channels kept), Plex and dizqueTV are checked
  from the new home (an address that was the old home's own is moved to its network address),
  Cloudflare's notifications move with the cloud relay, and the tab shows each machine and each
  outside service as moved, being moved, failed, or what a person must do (signing in to YouTube
  in a browser on the new home; a model runtime there, where the library used one). A switch that cannot go through is undone. `GET
  /api/cluster/self` carries the cluster's term.
- **A standby home can take over when the home is gone for good** (Epic 37, "Emergency
  promotion"). On the Machines tab the admin marks full machines as standby homes. Each one
  keeps the home's secrets and keys sealed to it alone. The home sends them again within two
  minutes of a change and removes them when the mark goes. With the home not answering, a
  standby's admin makes it the home from that machine's own Machines page, after a warning and
  the home's name typed to confirm: only from a whole copy that was within 10 minutes of the home
  when the home last answered, and only once the standby has claimed the term at the other
  machines, so two standbys never both take over (should two cut off from each other both take
  the same term, the one whose machine id is lower gives way, as an old home does, and every
  machine follows the other). The other machines find the new home at the
  standbys by themselves and follow it. If the old home comes back, it stops writing within a minute and
  becomes one of the new home's machines. What it wrote after the switch is kept aside and
  reported to the new home, where the admin downloads it or reviews and applies what does not
  conflict, in one transaction. Never applied automatically: a transaction touching a row the
  new home changed since, the secrets or the cluster's own data, a credential (a session, a
  token, a reset link, a pairing, a changed password), or one that would fail. A report carries
  up to 100,000 changes (more stay in the old home's database, which it keeps aside). What was
  applied is recorded. Each seal of a standby's kit has a key of its own. Migration 0147.
- **The cloud relay agent moves with the home.** The home's supervisor runs `maktaba-cloudlink`
  while `[cloud]` is enabled. A switch stops it on the old home, carries its credentials, and
  starts it on the new one. It ships with the Mac and Linux apps.
- **After a switch, a checklist on the new home**: what needs a person there (YouTube in a
  browser, a model runtime where the library used one, a Live TV tuner set by hand, Cloudflare's
  notification address on a home without the cloud relay) is one click from where it is done in
  the control centre, and ticked off by hand, or by itself where Maktaba can tell (a model runtime
  that answers, a notification that arrives).
- **The machines reach only what they need of the home's database** (Epic 37). The bundled
  Postgres listens beyond its own machine, and lets anyone in from off it, only while a machine
  may connect (one is approved, or joins are open). It starts again by itself when that
  changes. Even then it lets in only the machines' roles, from the networks the home is on and
  each approved machine's own address (`[postgres] machines_from` can name networks instead),
  and never as physical replication. Every machine verifies the home's database by its CA. The
  CA comes with the join, over the machines' channel (for a machine joined earlier), or with a
  switch; a machine without it does not connect its copy. A machine's replication password
  never leaves it (the home gets its SCRAM verifier), only full machines get a copy, and a copy
  holds web sessions by their hash only. A machine's replication role reads only what is
  published to it: Maktaba's Postgres builds have no logical decoding plugin but `pgoutput`, the
  slot functions are the home's alone, and the home ends and drops any slot that is not
  Maktaba's within seconds (saying once what to do about one it cannot). A home on an operator's
  own Postgres, which may carry other plugins, keeps no copies unless the admin turns on
  `rules.cluster.copies_on_operator_postgres`; until then the machines are told why. A sign-in on
  a machine while its home is away refuses a user who must change their password. The sessions it
  opens are written to the home's history, with their closing, also after a restart of the
  machine. A sign-out there is kept only for a session the machine knows, whole (a sign-out
  everywhere is kept with its session or not at all), handed over in batches the home takes, and
  the home ends only sessions made through that machine; a person signed in on the machine only
  while the home was away who signs out everywhere is signed out of every session on that
  machine and told the others end through the home (which would not take it from it); the home's
  revocations survive a restart of the machine during an outage, and a person signed out
  everywhere can sign in there again once its copy has it.
- **System health answers "is everything OK, and if not, what is wrong and what do I do"**. The
  page (`/admin/health`) reads the new admin-only `GET /api/admin/system/status`: every part of
  Maktaba ok, degraded or down with a plain reason, when it was checked and "Learn more" into the
  help. Services (the API with its version and uptime; Postgres with its latency, primary or
  replica and its replicas; the streaming service at its HTTP and gRPC addresses with the channels
  on air and encoder sessions; the pipeline's gRPC address; the supervised processes), Workers
  (every worker with its machine, role, last heartbeat and what it runs, and every streaming
  machine; a stale record of a machine that runs a newer worker is history, never a failure),
  Dependencies (Epic 40's checks of the model, each share, the disk and shared memory),
  Integrations (Plex Live TV: reachable, registered, channels in sync, last error) and Queues & TV
  (Epic 40's failing / waiting / needs-you rollup, the channels on air, the TV change waiting for
  approval, the live TV slot, the tuner). A banner says the overall state: Down only when the API,
  the database, every worker, or streaming while channels are on air is down. Every check runs at
  the same time under its own deadline and the answer is reused for a few seconds; the thresholds
  are the new rules section `system_health`. A red state puts a dot on System health in the admin
  menu. The public `/api/system/health` liveness probe is unchanged.
- **The desktop app's setup works end to end** (Epic 38, story 38.6; the Mac, Windows and Linux
  alike). `maktaba-server setup --json` gains every step the setup screens run: `check` (what will
  run, memory and disk), `admin` (the password is hashed as it is typed, by the new
  `maktaba-api hash-password`; only the hash waits), `libraries` (folders and the NAS path map,
  drive letters and UNC paths too), `models` (the Hugging Face token checked against the gated
  second-listen model, or the NAS copy found; the download after the venv, with progress),
  `plex` (the plex.tv PIN sign-in), `venv` (the pipeline's environment, with progress) and `finish`
  (server.toml, the database created and migrated, the admin seeded with the new
  `adduser --password-hash-file`, the Plex server stored with the new `maktaba-api plex-link`).
  The screens say "this PC" and "the tray" on Windows, and turn a disabled sign-in task back on.
  Every finding and refusal comes with a code and its values, so the screens say it in English or
  Arabic (never the step's English beside it); `check` separates what blocks from what is worth
  knowing and says when Windows or Linux will transcribe on the processor (no NVIDIA GPU). Library
  folders and both sides of the path map must be whole paths (a bare `Y:` is the drive's root), a
  password cannot hold a line break, and two spellings of one folder are one root only where the
  disk ignores case. A stop asks the venv build and the models download to stop instead of killing
  them under pip.
- **`maktaba-server setup` in a terminal runs the app's setup steps** (Epic 38, story 38.6): the
  same checks, admin rules, folders, path map, Plex sign-in and finish, plus where the data goes and
  the database (SQLite, which `finish` now creates, or a Postgres url). Its separate wizard is gone,
  with its admin token, which nothing ever accepted; `--migrate` is accepted and ignored (setup
  always prepares the database).
- **The desktop app's server takes care of an update at its next start** (Epic 38, story 38.12;
  the Mac, Windows and Linux alike). Before any role runs, the server reads the record of the
  release whose migrations the database holds (`state/database.json`) and refuses an older
  release; with another one (or no record yet) it dumps the database
  (`backups/pre-<old>-to-<new>.dump`, the newest `[postgres] update_dumps` kept, default 3),
  records the update, runs the migrations once for every role and records them done. When they
  fail it puts the dump back and keeps every role stopped, saying what happened and what to
  install. The pipeline's workers then wait for the venv bootstrap (not when `MAKTABA_PYTHON` or
  `[pipeline] python` names their Python), run with an environment of its own and stopped
  gently (SIGTERM, CTRL_BREAK on Windows) so a stop never leaves pip behind. A Postgres of
  another major than the database's is refused. What a process waits on shows as its `detail`
  in `/processes`.
  Each such detail also has a `detail_code` from a fixed list and its `detail_params`, for the
  app's screens to say it in their own language.
- **The API refuses a database a newer Maktaba migrated**, on every install: `maktaba-api
  migrate up` exits 3 instead of finding nothing to apply, and `serve` does not start on it.
  `migrate --timeout 0` runs with no time limit.
- **The Windows app moves in the Maktaba a PC already runs** (Epic 38, story 38.13). Setup finds
  it (a scheduled task that runs `maktaba-server serve` directly, through Maktaba's launcher or
  from a script; the node installer's workers; a server started by hand; a Windows service, which
  it names but cannot move) and offers to move it in: the plan shows what is carried and copied,
  and refuses, before anything stops, a key the app holds already and different, or a node whose
  node config is not the one the app reads; it says so when the roaming profile's node config is
  another PC's and this PC has not joined as its own machine (the app on this PC is not that
  node), the one rule serve and setup's check follow too.
  The move builds the venv while the old install still runs, keeps its task's definition, stops it
  (its script's restart loop too, and nothing of it may come back for longer than the loop's delay;
  never the app's own programs, wherever the old folder is) and disables its task, and carries its
  settings (addresses, media, path map, transcription, the script's `MAKTABA_*` settings), keys,
  search index, caches, models and an SQLite database into the app; a node's `node.json` stays
  where it is, the machine's one, which the app reads (it is never copied). The firewall
  rules that let other devices reach the old install's programs, in its folder or not, are copied
  for the app's own (Windows asks once, before anything stops; the plan says so; the prompt opens
  in front of the app's window, and one not answered within three minutes changes nothing); the
  plan names a rule of a program the app has none of, and warns of a block rule on an app's
  program (a block wins over an allow). The old task goes only once the app's server listens
  beyond loopback where other devices reached the old install (`0.0.0.0:8780`, say) and the rules
  are in place on the network in use. A move that stops part way can go on, or restore the old
  install from the screen, as can one done (the welcome screen). The app's server removes the old
  task once it ran healthy; the old folder is kept, and `maktaba-server adopt --undo` runs the old
  install again (the app's per-user task, and this user's of the old shared name, turned off; the
  move's firewall rules removed, the old ones never touched; the app opens its setup, which offers
  the move again, and never starts its server over the old install). Beside a node of a home a
  fresh setup is refused (check and finish say why): the app would be a second copy of that node;
  beside a node config nothing runs it is a new home (`[server] role = "home"`). An install on the
  default paths from before Epic 38 is one install whether its config and keys are still only in
  `%APPDATA%\Maktaba` or were copied into the app's place, and its state is read where it keeps
  it (its default data folder). Everything the plan and the move say comes with codes, so
  the screens say it in English or Arabic. `maktaba-server adopt` lists what it finds and `--plan
  <id>` what a move does. `[server] mdns = "off"` is new (as `serve --no-mdns`), so a moved install
  keeps that flag.
- **Transcription on Windows and Linux (story 38.8).** The transcribe stage runs Whisper on
  CTranslate2 there (faster-whisper's `dropbox-dash/faster-whisper-large-v3-turbo`), batched
  across files as the Mac's MLX engine does: on an NVIDIA GPU when CUDA works (cuBLAS 12 comes
  with the `stt` extra; no CUDA toolkit), else on the CPU. The supervisor sizes the workers and
  their batch from the GPU's memory (`nvidia-smi`; an 8 GB card gets one worker); without a GPU
  the main worker transcribes. New `[transcription]` keys: `device`, `compute_type`, `batch`,
  `cpu_threads`. Transcripts record their engine (`ct2-batched`); the second listen stays on
  the Mac for now, so while it is on a Windows or Linux machine leaves transcription to the
  machines that run it, unless the new rule Transcription, "Transcribe without the second
  listen", lets it transcribe too; a machine that runs the second listen then gives those
  transcripts theirs later. The operator's own stage exclusion always stands. A GPU under 4 GB,
  or one without the memory free, leaves Whisper to the CPU, as does a failed load on the GPU;
  more workers than planned share the GPU or the CPU; the Whisper download is pinned to a commit
  and its files' SHA-256; the free-memory floor works on Windows. On an RTX 2080 SUPER: 72x real
  time for eight files at once, 31x for one; 2-3x on the CPU.
- **The second listen on Windows and Linux (story 38.9).** Cohere Transcribe Arabic corrects
  Whisper's words there too, through transformers on torch's CUDA build on the NVIDIA GPU
  Whisper uses, respelt with the CAMeL dictionary as on the Mac (camel-tools now installs on
  Windows and Linux x86_64). It runs only where the GPU holds it beside Whisper (10 GB and up on
  Windows, where the driver keeps more of the card back; 8 GB and up on Linux) and has the room
  free when a file is done; else the transcript keeps Whisper's words and says why, as a Mac
  without the model does, and `models download` neither fetches the gated model nor asks for a
  Hugging Face sign-in for it. Never on the CPU. With it on, the supervisor gives Whisper a
  smaller batch on such a GPU to leave it room. Transcripts record what heard them
  (`metadata.refine.runtime`) and a missing dictionary (`dictionary: "unavailable"`). The
  dictionary's data folder is now made before CAMeL Tools downloads into it (a fresh models
  folder made it fail). It runs only where the GPU can (its memory, compute capability 7.5 and
  driver R580, read from the driver without importing torch) and its model can be had (the
  supervisor gives Whisper the smaller batch only then); its model is fetched at a pinned commit
  and checked file by file; a load that fails is not tried again for 10 minutes, doubling to 6
  hours.
- **torch's CUDA build on Windows, where the GPU runs the second listen (story 38.9).** PyPI only
  has a CPU torch for Windows (124 MB), which stays the default; the CUDA build
  (`torch 2.14.1+cu130` from PyTorch's own index, 2.0 GB; the venv grows from 2.7 to 5.0 GB) comes
  with a new `cuda` dependency group, only on a machine whose NVIDIA GPU runs the second listen
  (9.5 GiB or more, compute capability 7.5 or more, driver R580 or later, as `nvidia-smi` says).
  A release ships both pin sets, every pin hash-checked (`...-constraints.txt`, PyPI's alone, and
  `...-constraints-cuda.txt`, which names PyTorch's index: `uv export --emit-index-url`, so
  building a release needs uv 0.12 or later), named in `release.json` and `release.conf`; the
  venv bootstrap (`--torch auto|cpu|cuda`), `setup --node` and `install-node.ps1` (`-Torch`) pick
  one by the GPU; the Windows app's payload carries both (`pipeline\constraints-cuda.txt`). With the cuda set the node installers look for each pin on both indexes, and
  with the `stt` or `search` extra install the pins with the venv's pip, which takes the file a
  pin's hashes name from either index. Linux and macOS install the same packages as before. CI
  exports with uv 0.13.4. The semantic-search embedder no longer goes on an NVIDIA GPU of its own
  accord (`MAKTABA_EMBED_DEVICE=cuda` still puts it there): the GPU's sizing leaves it no room.
- **The desktop app runs the server on Windows** (Epic 38, story 38.3). When setup finishes on
  a Windows PC, the app registers the user's own sign-in task `\Maktaba Server <SID>` (the
  user's session, so mapped NAS drives work; each user of the PC has their own). The task runs
  `Maktaba.exe --server-agent`, which starts the bundled launcher in a console without a window
  and restarts it if it fails; a watchdog trigger every 5 minutes starts the agent again if it
  died. Pause, Resume, Restart and Stop server work from the tray through a stop event the
  launcher watches; a pause lasts until Resume or the next sign-in, and Resume right after a
  Pause waits for the server to finish stopping. A server whose agent runs but that does not
  answer for 3 minutes shows as a problem with the reason from its log. An update stops the
  server before the installer replaces its files, and starts it again if the install fails.
  The app's mode is kept per PC (`%LOCALAPPDATA%`), not in the roaming profile. Setup steps and
  `schtasks` start without a console window, and the installer's hooks get `--server-start`,
  `--server-stop` and `--server-remove`.
- **The tray says what the server waits on** (Epic 38, story 38.4). While the server starts, the
  tray's status line and tooltip, and the window's starting screen, show what the server reports a
  process waits on or why it cannot start: each process's `detail` in `/processes`, which the
  server fills from story 38.12 (#194) on; a failing process's own reason comes first. The
  starting screen says it in English or Arabic from the code and values the server sends with the
  detail (every code of story 38.12's list: the database step, Postgres, the transcription tools),
  with the programs' own error messages, the log's path and the server's English under Details,
  and says only that the server is starting when the app has no words for the code or the server
  sends none. The tray and the app's error dialogs speak the system's UI language, English or
  Arabic; the tray shows the server's English detail only in English. On Windows and Linux the
  tray icon carries a status badge (red a problem or a server that does not answer, else violet an
  update ready, else green running, amber starting, grey paused or stopped; each 3:1 or more on a
  light and a dark taskbar), and on Windows a left click opens the window (the menu is on the
  right button).
- **Postgres programs for every platform, and Postgres on Windows.** Maktaba can fetch the pinned
  Postgres 16.15 build of the machine's platform (theseus-rs; size and SHA-256 pinned, unpacked
  with path checks), and the supervisor runs Postgres on Windows (its `.exe` programs, a fast
  shutdown through `pg_ctl` instead of a kill, and `postgres.exe` under a restricted token when
  the server runs as an administrator, as the Windows service does: Postgres refuses
  administrative rights). Groundwork for the Windows and Linux apps and for full nodes' copies of
  the home's database (#178).
- **The Windows app runs its own Postgres** (Epic 38, story 38.10). `[postgres] enabled = "auto"`
  now turns the bundled Postgres on inside any desktop app's payload, not only on a Mac (a
  `[database] url` or a SQLite database in use still wins). The app's roles find the payload's
  `ffmpeg.exe`. Loopback TCP only on Windows, the nightly dumps as on the Mac.
- **Maktaba on a Linux machine without a desktop** (Epic 39, story 39.6). `install.sh --app`
  verifies the release first (with the machine's cosign when it is 2.4.3 or newer, else a pinned
  cosign it fetches), then `maktaba-server setup` asks the desktop app's questions in the terminal,
  in English or Arabic: the server is copied into `~/.local/share/maktaba/runtime`, then runs as
  the user's systemd service, started at boot through linger. The check also reports glibc, the
  systemd user manager, linger and a Maktaba service another install owns; where the second listen
  cannot run, no Hugging Face sign-in is asked. When the distribution's ffmpeg is missing or falls
  short, the terminal offers to run the command check names (sudo asks for the password), then
  checks again; from an answers file it only shows it. A Plex code that expires (plex.tv out of
  reach is asked again until then) or a Ctrl+C while it waits leaves Plex to Settings, and the
  setup goes on. `--answers` (or `--answers-fd`) gives every answer without questions.
- **The Linux app's downloads** (Epic 39, story 39.10). Each release builds the app as an
  AppImage, a `.deb` and an `.rpm` for x86_64 and aarch64, each with the whole server inside
  (its own Postgres and Python; ffmpeg is the distribution's, which the `.deb` and the `.rpm`
  depend on), checked before anything is published; the `.deb` and `.rpm` stop the app's server
  when they are removed. For machines without a desktop the release carries the server payload
  (`maktaba-linux-payload-<version>-<arch>.tar.gz`) and `install.sh` among its assets, both in the
  signed `checksums.txt`: the notes say to verify the script and run it, no longer to pipe it from
  a branch.
- **The desktop app runs the server on Linux** (Epic 39, story 39.8). When setup finishes, the
  app copies its server into `~/.local/share/maktaba/runtime` and runs it as the user's systemd
  service, started at boot through linger; after an app update it moves the server to the new
  version at its next start (an app older than the server leaves it alone and says so). Pause,
  Resume and Stop server work from the tray, or from the window's Server menu where the desktop
  shows no tray; the tray starts at login when it can be shown. The tray, the Server menu and the
  app's dialogs follow the session's language (English or Arabic).

### Changed

- **A machine that loses its home stays usable** under a notice instead of the whole app being
  swapped for the "cannot reach its home" page (that page stays for a machine whose code does
  not fit the home's database, and for one with no usable copy). This replaces the home's last
  sign-in answers a node used to keep for ten minutes: `rules.cluster.offline_read_minutes` is
  gone.
- The Postgres bundled with Maktaba.app keeps its TLS certificate and key in the keys folder
  (never in the database folder a machine's copy is made of).
- **The Linux app runs the distribution's ffmpeg** (Epic 39). The Linux server payload carries no
  ffmpeg any more (the builds it pinned were configured `--enable-nonfree`), and `payload.sh` drops
  every download `pins.env` no longer pins from its cache. The ffmpeg every role runs is
  `[media] ffmpeg` / `ffprobe` in `server.toml` (new), else `MAKTABA_FFMPEG` / `MAKTABA_FFPROBE`,
  else the Mac's or Windows' app's own, else the PATH's, and all roles get that one. Setup's check
  (Linux), the new `maktaba-server ffmpeg` and the System health page say whether it does what
  Maktaba needs (FFmpeg 4.4 or later with libx264, AAC and the filters and formats the TV and the
  pipeline use, ffprobe beside it, and an H.264/AAC encode through `speechnorm` that works) and,
  when not, the command that installs the right one for the distribution: apt, dnf with RPM
  Fusion (Fedora's and EPEL's `ffmpeg-free` has no libx264), zypper with Packman, pacman. It also
  warns when chromaprint (the audio fingerprints) or https:// sources are missing.
- **Every role stops cleanly on Windows.** The supervisor starts each child in a process group of
  its own and asks it to stop with CTRL_BREAK (the Go roles drain on it as they do on SIGTERM, the
  pipeline on `SIGBREAK`); a child is killed with its process tree only once the shutdown grace
  is over. Postgres keeps its `pg_ctl` fast shutdown. A `serve` with no console (started detached
  by a program that has none) cannot send CTRL_BREAK, so it kills its children as before and says
  so once in its log; its children then open no console window on the desktop. A child is held
  still while it is asked, so the CTRL_BREAK never goes astray to `serve` itself; so are the
  venv bootstrap the server and setup start, and setup's model download, when they are asked.
- **Windows installs point at the Maktaba app** (Epic 38, story 38.14). The README's install
  section, the deployment page, the packaging and desktop READMEs describe the Windows app (one
  NSIS `-setup.exe` with the server inside, for the current user; no `.msi`), and the node
  installer (`install-node.ps1`) says in its help and its summary that the app runs a full node
  and moves a joined worker in.
- **Windows keeps everything in `%LOCALAPPDATA%\Maktaba`** (Epic 38, story 38.1), for a new install:
  `server.toml`, `keys\`, `data\`, `logs\` and the node's `node.json`, with the models, caches
  and the bundled Postgres beside `data\`. A new install puts nothing in the roaming profile.
- **An install from before keeps its originals.** Its next `maktaba-server` command copies
  `server.toml` and the keys there from `%APPDATA%\Maktaba` (with or without a `server.toml`;
  nothing with `MAKTABA_CONFIG` set), and the copies are used from then on.
  - The originals stay in `%APPDATA%\Maktaba`, which roams with a roaming profile: older
    versions of the install, a node's pipeline run from a checkout and the node installer still
    read them. `uninstall --purge` removes them with their copies.
  - The copied keys and passwords are limited to the user, as `node.json` is.
  - A copy is never replaced. One that differs from its original stays, and the command says so
    once.
  - Each copy is written beside its new name, flushed, compared and put in place without
    replacing anything, so a roaming profile on another drive or on a server works, and a copy
    cut short is made again by the next command.
  - One command at a time copies: a lock in `%LOCALAPPDATA%\Maktaba`, and a second command
    waits for it.
- **`node.json` is never copied.** A machine has one node config: one that joined before keeps
  it in `%APPDATA%\Maktaba`, where every reader (Maktaba, streaming, the pipeline, the node
  installer) and a re-join use it. That profile can roam, so the file records the PC it belongs
  to (`machine_id`, Windows' MachineGuid). The first new Maktaba that runs with it on a PC claims
  it, keeping its ACL; the pipeline is its one writer. A replace that fails half way never loses
  the file: the old one is put back, or the new copy takes its place. What even that could not
  finish (the old file under a backup name, the new one as a temp file) is still the node
  config for every reader, so a roaming one stays this PC's and the node stays joined; the next
  start puts it back without ever replacing a file a write put there meanwhile. A re-join keeps
  it as `node.json.bak` and removes the rest, and the node's uninstall script names it among
  the files to delete to forget the home. A claim that cannot be written leaves the file as it
  was and says so once. The temp file a node config is written through is created the user's
  alone, with no other handle open on it, and when that cannot be done the credential is never
  written. A PC it does not belong to skips it: it is no home's machine, and its own join
  writes `%LOCALAPPDATA%\Maktaba\node.json`, never the other PC's identity. `maktaba-server`
  says so once at its start, and the app's setup check shows it (`node_config_other_pc`).
- **The large state stays where it is.** The install's models, caches and Postgres stay in
  `data\`. It is decided once whether `data\` or the folder beside it is the state root, and the
  answer is kept (`state-root.json`), so a folder an older version makes later never moves it.
  Nothing moves or copies a folder that a running Postgres or an older version may hold.
- **Inside the desktop app nothing is copied or moved.** A `maktaba-server` inside the app's
  payload, on Windows or a Mac, never copies or moves an older install: that comes into the app
  only through the app's own move, which copies it and keeps the old one for going back.
- The node installer's `node.env` stays where its scripts read it.
- The api and the pipeline, run on their own, read `keys\secrets.key`, or the old one until it has
  been copied.
- On Windows the pipeline writes its logs in UTF-8 (`PYTHONUTF8`), so an Arabic title can no
  longer raise `UnicodeEncodeError`.

### Fixed

- **A TV held on its old streaming software stays there through schedule and settings
  changes.** While an update's TV check had failed and the live TV was held on v0.3.39, the
  0.3.40 pipeline recorded its own `0.3.40` on every schedule release: staging ran the new
  binary, and swapping the change in would have put the held TV on the software whose check had
  just failed. A change that brings no binary of its own now records, stages and swaps on the
  streaming software the live TV runs. The TV moves to new streaming software only when a
  binary change passes its check: the update's Retry, or a later release's own. A staging slot
  asked to run a release whose binary is not on the machine is refused at once, and the change's
  staging fails with that reason instead of running other software.
- **A TV schedule change's check no longer fails a staging slot that runs the right
  software.** A release the pipeline builds for a schedule or settings change records its
  streaming version as `0.3.40`, the slot reports `v0.3.40`, and the check compared them as
  text: TV change #51 (2026-10-28) failed "the staging slot did not become healthy on the
  release" three times with the slot up on that very binary. Versions now compare without the
  `v`. A release recording no binary (a pipeline run from a source checkout records `0.0.1`)
  stages and binds this install's streaming binary instead of asking for one that does not
  exist.
- **An update's TV check no longer overloads the host it measures.** TV change #55 (0.3.39 →
  0.3.40, 2026-10-28) failed channel.position and channel.wallclock on all 28 channels, 38–50 s
  behind the wall clock, and held the TV on 0.3.39. The measurement was right and the release's
  encoding was the same as live's: a binary change changes every channel, the staging budget was
  the whole quiet host, and the staging slot started all 28 channels at once. Their output then ran
  at 0.2–0.7x real time and fell further behind every second (−80 s at 100 s on a later run), so
  no settle time could have passed it. The changed channels are now probed in a window that
  starts at 4, doubles only while every staging station is on the wall clock, halves as soon as a
  station's output runs slower than real time, and never again reaches the size that overloaded
  the host. The checks are unchanged: a release that cannot keep up even alone still fails.
- **An update's TV check no longer fails a Mac's new streaming software on its warm-up.** The
  staging check of TV change #53 (0.3.37 → 0.3.38, 2026-10-28) failed 27 s in and rolled the
  update back, for three reasons none of which was the release: the staging slot's channels, all
  started at once, opened their encoders 7–15 s late and were judged 10 s after their first
  segment, when ffmpeg 7.1+ (`-re`) makes such a lag up at only 1.05× (four minutes); VideoToolbox
  put a keyframe every 4 s where the 2 s splice grid (and the check) want one every 2 s; and a
  filler airing in place of a programme whose file is missing on this machine counted as the wrong
  programme. Now a lagging input catches up at 2× (`-readrate_catchup`, when the ffmpeg has it),
  VideoToolbox keeps the keyframe interval like libx264 and NVENC, `channel.wallclock` and
  `channel.position` give a channel 30 s from its first segment to settle (they warn and hold the
  soak meanwhile, and still fail a channel that stays behind), and the slot says which programme
  a filler stands in for, so `channel.block` warns about the missing file instead (and still fails
  when live reads that file). The TV staging end-to-end test waits for a failed change in draft,
  where a failure has gone since story 33.12.
- **A full node plays the videos it can read itself again.** A node's path map keys the prefix
  the database stores (`{"/Volumes/Originals/": "O:/"}`, inferred from the home's mounts and the
  node's), and its streaming role opens files through it, but the api's check of whether this
  machine can read a video kept only `smb://` keys. Every video looked unreadable on a Windows
  node with the share on `O:`, every session went to the home instead, and playback buffered
  there. The api now reads a node's map as its streaming role does. And a transcode that ends
  before it writes its playlists answers 410 for them, not a 404 the player waited on for good:
  it opens the video again, or says it cannot play it.
- **The video page has a Back button.** The desktop and TV apps' windows have no browser Back:
  the video page now goes back to the series, search or list it was opened from (a pasted link:
  the library).
- **"Help improve Maktaba" keeps an earlier "Not now".** Since the usage data goes to Maktaba
  rather than the home's admin, the old answer was forgotten so a yes would be asked again; a no
  was forgotten too, and the notice came back for people who had already declined. A no now
  carries over, dated as it was.
- **A Windows node logs in to the home's database again.** Since passwords left the URLs a
  role gets (a passfile instead), the api and the copy of the database on a Windows machine
  logged in with no password: lib/pq before v1.11 skips a passfile it takes for readable by
  others, which on Windows is every file. Videos, series and the TV guide answered "the home's
  database is not reachable". lib/pq is v1.11.2 in the api and the server, and a `PGPASSWORD`
  in the server's own environment no longer replaces every role's password.
- **A viewer's web session now sees the libraries they are granted.** The session cookie's
  sign-in carried no library grants, so a non-admin user saw no library at all in the web app
  (the apps' tokens were right).
- **A library folder entered as the database stores it is found through the path map.**
  Adding `/Volumes/Originals/...` on a Mac that mounts the share elsewhere was refused as
  `not-found`.

- **Indexing for search is about 3 to 5 times faster again.** Chroma, by default,
  rewrites its whole index to disk every 1000 vectors: on the home Mac's 12 million
  vectors that is a 21 GB file and a 1.2 GB id map, seconds each time, and it held the
  index worker to about 120 vectors a second (3–4 index jobs a minute). The collection now
  rewrites every 10 000 (`MAKTABA_CHROMA_SYNC_THRESHOLD`, set once on the collection; 0
  leaves Chroma's own), which measured about 600 a second on a copy of that index. An
  index job no longer scans the collection: the vectors of a video's old transcripts are
  deleted by their ids (from the database), not by a delete over the whole collection
  (about a second each), and `chapter_infer` reads a video's vectors by id (0.01 s, not
  0.8 s). The embedding runs outside the search index's write lock, and an index already
  open is used without it. Each index job logs where its time went (`index_timings`: the
  embedding, the writes, the wait for the write lock, the database read), also in its
  summary.
- **Stopping the server no longer breaks the search index.** The index worker stopped
  with 27 index jobs in flight: it noticed the stop 9 s late (the claim loop waited out its
  poll), cancelled them after 15 s while their Chroma writes ran on in threads, then did not
  exit (Python waits for those threads) until the supervisor killed it, mid-write. Now every
  write to the search index runs alone (one at a time per process) and is never cut short: a
  job cancelled during one ends once it is written, and once the worker stops no new write
  starts (the jobs waiting for one end at once, as interrupted). The claim loop ends its wait
  the moment a stop comes. After its runtime stops the worker closes its Chroma client and
  exits without the threads still running. The index worker gets a 30 s drain
  (`MAKTABA_PIPELINE_DRAIN_SEC`) and a 45 s stop grace of its own (`Process.StopGrace`,
  also for a restart and a hung worker), within the launcher's 60 s.
- **The search index no longer breaks when the media share stalls.** The index and
  `chapter_infer` stages and the meaning (semantic) search ran in the main pipeline worker,
  beside the stages that read the NAS share; a share that stalled got that worker killed for
  not answering, and a Chroma write cut in half left the index unusable ("Error constructing
  hnsw segment"; three times on the home Mac). On a home the supervisor now runs them in a
  pipeline worker of their own (`pipeline/index`, `MAKTABA_PIPELINE_STAGES=index,chapter_infer`)
  that never touches the share (`MAKTABA_PIPELINE_SHARE=off`: no storage checks, share probes
  or path conversion) and serves the semantic search on its own gRPC port (127.0.0.1:9093, logs
  and liveness on 127.0.0.1:9104; `[server] pipeline_search_addr` / `pipeline_index_logs_addr`),
  which the API asks (`MAKTABA_PIPELINE_SEARCH_ADDR`). The main worker leaves those stages to it
  and opens no Chroma (`MAKTABA_PIPELINE_SEMANTIC=off`): one process per Chroma directory, as
  before. `[pipeline] index_worker = "off"` keeps the old layout; an operator who excludes
  `index` or lists the stages by hand (`MAKTABA_PIPELINE_STAGES`), and a node, get none.
- **The first video played right after Maktaba first starts is no longer refused.** The API makes
  the machine's stream signing key as it starts, and the streaming role looked for it every two
  seconds: a signed stream URL in between was refused ("bad signature"), and so was every one
  after the key file was replaced. A URL naming a key the streaming role has not read yet now
  reads the key file again at once (at most every 100 ms; a request that finds that read under
  way waits for it), and the key a replaced file held still verifies the URLs it signed, for a
  day.
- **Video transcodes work on an NVIDIA GPU (and are found on Windows).** The VOD transcodes
  (HLS and DASH, Story 8.5) passed `-hwaccel nvenc`, which FFmpeg does not have (NVENC is an
  encoder; NVIDIA decoding is `cuda`), and libx264's `-preset veryfast` to every encoder, which
  NVENC refuses: on a machine with an NVIDIA GPU every transcode failed at once. Now the GPU
  decodes H.264 and HEVC sources (`cuda`, VideoToolbox on a Mac; AV1 and the rest on the CPU, as
  a GPU without AV1 decode cannot fall back), every rung is 8-bit 4:2:0 (a 10-bit source too:
  NVENC's H.264 refuses 10-bit, and browsers rarely play High 10), and each encoder gets its own
  preset (`p4` for NVENC). NVENC sessions are counted with the TV's channels', one per rung:
  together they stay within the channels' NVENC cap (with what the TV slots hold), VOD holds at
  most half of it, and the rungs it gets none for are encoded by libx264. A hardware run that
  fails within 30 s is run again in software. The encoder is chosen by a short test encode, on
  Windows too (it was always software there), detected again every 5 minutes while a listed GPU
  encoder failed, and `MAKTABA_HWACCEL_PREFER` (`auto`, `software`, `nvenc`, `qsv`,
  `videotoolbox`) narrows it; the transcodes run the ffmpeg the channels run
  (`MAKTABA_FFMPEG_BIN`). Each rung of an HLS transcode is now its own variant (`v1`, `v2` were
  never written, so the master playlist's lower rungs 404'd), a source without audio transcodes,
  and a session asking for software (`force_software`) gets it.
- **The second listen's tries count from the end of each try, and a stopped transcript comes
  back (follow-up of #221).** The wait between tries counted from when the try was queued, so a
  job that waited hours used it up; it now counts from the end of the try. A transcript the
  second listen stopped on gets its tries again by itself, once, when the home sees a
  second-listen model, or a machine's GPU, driver or packages, for the first time, and Settings,
  Transcription has the number of tries, the waits and a round to raise to try them all again
  (new table `relisten_seen` and columns on `relisten_attempts`, slot 0146). A try that found no
  room on the GPU no longer counts (follow-up of story 38.9). Deleted and missing
  videos are no longer tried, and the stop note keeps its reason on long Windows paths.
- **The Linux runtime copy orders versions as everything else does.** It kept its own reading of
  `git describe` builds, so a dirty build of a tag ranked above the next commit's build and a dirty
  build equalled the clean one: it now uses the one order (`v0.2.0` < `v0.2.0-dirty` <
  `v0.2.0-1-g…` < `v0.2.0-1-g…-dirty`). The self-managed layout's order also puts a version that is
  no release (`dev`) below every release, as the release verifier does, and compares prerelease
  names whatever their case.
- **`[server] role = "home"` (or `MAKTABA_ROLE=home`) runs no role as a node.** The supervisor
  only left the node's variables out, while streaming and the pipeline read the node config
  themselves: beside a joined `node.json` (on Windows another install's, through the roaming
  profile's fallback) streaming ran as that node. Every role is now told it is the home
  (`MAKTABA_CLUSTER_ROLE`, `MAKTABA_TV_HOST_ROLE`) and pointed at a node config that is never
  written, and the forced home runs its own bundled Postgres and TV slots whatever `node.json`
  holds.
- **The second listen's retries back off and stop (follow-up of story 38.8).** A machine that
  runs the second listen queued the same 20 oldest transcripts made without it every 10 minutes,
  forever, when it could not give them theirs (a file it cannot read, a model that does not load),
  so newer ones never got their turn. Each video's tries are now recorded (new table
  `relisten_attempts`, slot 0145): after a try it waits 1 hour, doubling up to a day, the untried
  videos go first, and after 6 tries it stops and says so once, as a warning in Processing,
  Activity. A transcript with the second listen, or a new transcribe run, starts over. The
  model download's hash mismatch names the hashes before the folder, so a long Windows path no
  longer cuts them off. The Settings page shows the Transcription rules in Arabic, and a check
  keeps every rules section in Arabic but those not yet translated.
- **`maktaba-server setup` (the CLI wizard) seeds the admin again.** It called `adduser` with
  `--username`/`--password` flags the api does not have; it now passes the username and a
  password file.
- **Setup asks for a Hugging Face sign-in only where the second listen runs.** It asked whenever
  the second listen was on, so a PC without an NVIDIA GPU, with one too small or too old, or with
  a driver before R580 was made to create a token and accept a gated model's licence for a model
  that never runs there. `check` now says whether this machine can run it (and why not, in the
  app's words), the models step no longer asks for the token there (the NAS copy can still be
  chosen, any model the home published), and the download takes only Whisper and the search model. The
  graphics card's compute capability and driver version come with its name and memory.
- **The terminal setup's Plex sign-in can be skipped, and survives a moment without plex.tv.**
  Ctrl+C while it waits for the code skipped nothing: it stopped the whole setup before `finish`,
  as did a single poll plex.tv did not answer. Ctrl+C now skips the sign-in and the setup goes on;
  plex.tv not answering is said once and asked again until the code expires; a second Ctrl+C
  elsewhere ends the setup even at a question. Every code a setup step fails with now has its
  words in English and Arabic (a test keeps it so): the language model's, a bad request's, an
  unknown step's, unreadable settings', an unexpected failure's.
- **After the app's setup the server runs on the bundled Postgres, with its transcription
  workers.** Saving `server.toml` wrote every default, so setup's `finish` left `[database] url`
  set to the default SQLite file: the next start never ran the bundled Postgres (the database setup
  had made sat unused, and the pipeline could not open the SQLite one), and
  `[pipeline] instances = 0` turned the workers off. A save now writes the url only when one was
  chosen and the count only when it was set (`setup --node --instances 0` included).
- **A user's server keeps its update verification current.** `maktaba-server` gave the api and
  streaming no data directory, so they used `/var/lib/maktaba`: in the desktop apps and every
  per-user install the daily refresh of the Sigstore trusted root failed (permission denied), and
  once the root built into the binary expired, Update now could no longer verify a release.
  Uploaded channel art had the same problem. The supervisor now passes its `data_dir`
  (`MAKTABA_DATA_DIR`), and an api or streaming server run on its own uses `/var/lib/maktaba`
  only as root, else the user's `$XDG_DATA_HOME/maktaba`.
- **A build made from a checkout is never "older" than the release it was built from.** A
  `git describe` version (`v0.2.0-12-gabc1234`, the version of a build off a tag) ranked before
  `v0.2.0`, as a prerelease does: `maktaba-server update`, the API's "update available", the
  Updates page and the scheduler offered `v0.2.0` as an update to it, the verifier refused
  updating from it as a downgrade, and the self-managed layout's watermark and setup's conversion
  took it for a downgrade. It now ranks after its tag (by its number of commits, a dirty tree
  after a clean one: `v0.2.0` < `v0.2.0-dirty` < `v0.2.0-1-g…`) and before the next release, and
  is not a prerelease when its tag is not. One order everywhere: the release verifier's, which
  the update catalog, the API's update check and the self-managed layout follow, and the Updates
  page's pin check matches.
- **The launcher never sends CTRL_BREAK to a server that has exited** (Windows, Epic 38 story
  38.2). It holds a handle to the server until it is reaped and asks it to stop only while it
  still runs, holding the server suspended from that check to the send so it cannot exit in
  between: a CTRL_BREAK naming a pid that leads no process group (a server that exited as the
  launcher was stopping, or as it rolled back a release that timed out) goes to the whole console
  and can end the launcher itself.

- **Release builds for desktop, Android and Android TV.** The desktop app ships a
  (placeholder) icon set (#152), which the macOS and Windows bundles failed on. The Linux build
  stopped earlier: `tauri-plugin-deep-link` 2.6.2 does not compile against `rust-ini` 0.21.3,
  so the desktop app pins `rust-ini` to 0.21.1 on Linux (`Cargo.toml` and `Cargo.lock`). The
  Windows portable `.zip` step finds the exe under the name the Tauri CLI gives it
  (`mainBinaryName`, else the Cargo bin name) instead of a hard-coded one, and a
  pre-release tag such as `v0.3.0-rc.1` gives the MSI the numeric version WiX requires. The
  Android and Android TV jobs no longer ask sdkmanager for the removed `tools` package.
- **Unsigned Android builds stay off the release.** Without the keystore secrets the APK and
  AAB are kept as workflow artifacts (named `-unsigned`) and the run warns; only signed builds
  are attached to the GitHub Release, since Android won't install an unsigned APK.
- **Relay deploys only where a VPS is configured.** A tag push rolls out the relay only when the
  `RELAY_DEPLOY` repo variable is `true` and the image built and pushed; otherwise no
  `production` deployment is recorded.
- **Web image release build.** The web image builds its bundle natively instead of under arm64
  emulation, where `pnpm install` hung past the release timeout.
- **The pipeline's venv on Windows: a rebuild or repair waits for a file held open (story
  38.7).** Removing the old venv and `venv --upgrade` writing over `Scripts\python.exe` failed
  outright when another process had a file of the venv open: a virus scanner or the search
  indexer, or a program of the venv still exiting (WinError 5 / 32, Errno 13). Each is now tried
  again for up to 5 s, as the status file already was, and a read-only file is made writable
  before it is removed.
- **A pre-release no longer moves the images' `:latest`.** A tag with a suffix (`v0.3.0-rc.1`)
  pushed every server image, and the relay's, as `:latest` too, so a Docker install following
  `:latest` would have taken a release candidate. `:latest` now moves only with a plain `vX.Y.Z`,
  as the GitHub release's latest does, and a pre-release tag no longer rolls the relay out to the
  VPS by itself (a dispatch with its tag still does).
- **Signing out everywhere on a machine whose copy does not answer, and a late thaw** (Epic 37,
  follow-up of #178). A person signed in on a machine only while its home was away who signed
  out everywhere, at a moment the machine's copy could not say whether they had signed in
  through it at the home, was treated as signed in there only: the home was never told and the
  sign-in page said so wrongly. The machine's sessions end as before, the sign-out goes to the
  home all the same (it ends the other sessions if they signed in through that machine), and the
  sign-in page says those were not checked. And a thaw still owed by a switch that was called off
  or failed no longer makes the home writable while a later switch has it frozen: a thaw of a
  switch another came after changes nothing, and a freeze and a thaw run one at a time.
- **Node config writes no longer undo one another on Windows, and a node.json an editor saved
  is kept** (story 38.1, follow-up of #231). Writes of one node config, and the start that puts
  back what a write that could not finish left, now run one at a time (a lock beside it,
  `.node.json.lock`): two ReplaceFileW calls on one file at once could put an older node.json
  back over a newer one. A start puts a backup back only without replacing a file there, only
  after looking at `node.json` again, and never an older backup while a newer one is busy. A
  `node.json` saved with a UTF-8 BOM (Notepad, PowerShell 5.1) is the node config for every
  reader, and a broken one a backup replaces is kept beside it (`node.json.broken-…`), never
  removed. A start that cannot make the lock (a folder it may not write, a full disk) leaves the
  leftovers and reads past them, as before.
- **Video transcodes share the GPU with the TV, and HDR plays right** (follow-up of the NVIDIA
  transcode fix). The TV comes first: a transcode takes NVENC sessions only beyond what the
  channels need (two for each on air and each the hot standby keeps warm, and room for one more
  channel to start, live and warm, with TV slots on or off, every channel engine in the process
  counted), and at most half the channels' cap; the rest of its rungs are encoded by libx264. At
  the default cap of 6 with TV slots on, a transcode therefore gets no NVENC session while a
  channel is on air (two with none on air), and its rungs run on libx264: about twice real time
  for 4K HDR on an i9, and a slower host may not keep up. A failover during a transcode is still
  warm, and a channel started during one finds its sessions; beyond that, until the transcode
  ends, a channel may run on libx264 and the standby keep fewer warm. With TV slots on, those
  sessions are counted live: the front door lowers the slots' budgets by them before they are
  taken and keeps them only when the live slot took its new budget and the slots' fresh reports
  leave room, so VOD's sessions and the live slot's stay within the cap (the standby's warm
  channels, and a release's staging slot during a swap, hold theirs on top, as without a
  transcode); a slot that has just started holds to half its cap until it hears its budget. A
  transcode's open keeps to its deadline (the sessions' sync gets the request's), and a source
  whose colour probe fails is logged and encoded as SDR. VideoToolbox keeps to its own cap. A GPU
  encoder that failed its test encode is detected again with growing waits (a busy one until it
  works, one the machine does not have a few times). HDR (PQ or HLG, MPEG-TS too, and sources
  that name only their transfer) is tone-mapped to SDR when ffmpeg has `zscale` and `tonemap`,
  after scaling to the top rung (4K HDR transcodes at twice real time instead of below it), with
  Möbius' curve, and a transcode whose tone-mapping fails plays untoned instead of not at all.
  Every rung has a key frame on each 6 s boundary, so its segments align with the other rungs'
  (DASH, one AdaptationSet, says so).
- **maktaba-server writes node.json the way the pipeline does, every node.json is its user's
  alone, and no reader takes a write under way for "not joined"** (story 38.1, follow-up of #178
  and #239). maktaba-server's own writes of the node config (a switch of the home, a standby
  taking over, the home's database CA) gave the file its folder's ACL, so on a second drive every
  signed-in user could read and change the node's private key; they ignored the pipeline's write
  lock, so the two could undo each other; they wrote back a whole copy read earlier, undoing what
  another write put in meanwhile; and a `node.json` saved with a BOM lost what only the pipeline
  writes (when the machine joined). They now go through one writer that follows the pipeline's:
  its lock, a fresh read under it that changes only the fields the write is about (and stops,
  leaving the file as it is, when the machine joined again meanwhile), a temp file the user's
  alone from the moment it exists, ReplaceFileW, and the pipeline's names for what a write that
  could not finish leaves. What is guaranteed now: after any write by either writer, and after
  the pipeline's first load in a process, `node.json` is its user's alone. One #178 left with its
  folder's ACL, or any wider one, gets a protected DACL that grants its user and no one else, and
  on Linux and macOS one others may read is set to mode 0600; a write that cannot make it so
  fails rather than keep the wider ACL. A broken `node.json` kept aside (`node.json.broken-…`),
  and the identity a home's switch or a re-join sets aside (`node.json.was-node-…`,
  `node.json.bak`: the old private key), are made the user's alone too, whatever ACL the file
  they were had. A switch's records and the machine identity kept for it are written the same
  way, an identity an older version kept is made the user's alone when it is read, and the temp
  files a write of them left when its process stopped go with the next write. And no reader (the
  pipeline's load, maktaba-server's, streaming's role and node checks) takes a `node.json` that
  is being replaced, or that another process holds open for a moment, for a missing one: while
  the write lock was taken in the last minute, or a write's leftovers of the last minute are
  beside it, it waits up to 2 s for the write to end, and a write still under way after that is
  an error, as is a node config it cannot read or decode: never "not joined" and never the home,
  and the path stays the roaming one rather than falling back to `%LOCALAPPDATA%`. Streaming
  refuses to start on one, and serve starts nothing of a home on a node config it cannot read: no
  role (the api, streaming, the pipeline), no bundled Postgres cluster, no machine agent, rather
  than start them as the home against this machine's own database. So a machine that never
  joined, beside a node config lock file made in the last minute (a write that stopped half way),
  now gets that error for up to that minute rather than starting at once: serve says so and
  checks again every 5 s, and starts once the write ended or the lock is a minute old. A file
  another program holds open for a moment (an antivirus, a backup tool) is waited out the same
  way. A home's switch that moves `node.json` aside first removes every leftover of a write, and
  fails before leaving a whole node config readable, so the machine that is now the home is never
  read as a node again; a broken `node.json` there is kept aside as `node.json.broken-…`. Every
  reader decodes alike: UTF-8 with or without a BOM, UTF-16 or UTF-32 with one (PowerShell 5.1's
  `Out-File` and `>`), and text that does not decode strictly (an ANSI file) is broken for all of
  them. A broken `node.json` kept aside goes after 30 days and at a re-join (it may hold part of
  a credential), the write lock and an identity a switch set aside are among the files the
  uninstall script lists to forget the home, a write and a re-join try again for a moment while a
  reader has `node.json` open, and the identity a switch set aside is found beside the roaming
  `node.json` as well as the local one.
- **The Linux app builds for a pre-release tag.** `v0.3.0-rc.1`'s desktop release failed on both
  Linux legs: rpm takes no `-` in a version. A pre-release's .deb is now `0.3.0~rc.1` and its
  .rpm `0.3.0` release `0.1.rc.1`, both sorting before `0.3.0` (the .deb's `0.3.0-rc.1` would
  have sorted after it, making the release a downgrade); the app and its AppImage keep
  `0.3.0-rc.1`, the semver its updater compares with latest.json, and the .deb and the .rpm are
  signed for it. CI's Linux app gate now builds a pre-release (`v0.0.0-ci.N`) on every run,
  through the same script. The server's own .deb and .rpm (nfpm) already carried
  `0.3.0~rc.1` inside, `.rc.1` only in the files' names, as GitHub names them; CI's launcher
  gate now checks that form and its dpkg and rpm order before the release.
- **A transcoded video can be sought through and played to its end.** A video transcode's
  playlist kept only its last six segments (DASH: ten) and deleted the rest, so a transcode
  running ahead of the player deleted segments before they were played, and nothing outside the
  window could be sought to. Its playlists now list every segment: HLS as an EVENT playlist that
  ends with `#EXT-X-ENDLIST` when the transcode is done, which players start at its beginning
  (`#EXT-X-START`) unless they resume elsewhere; DASH without a window, its MPD dynamic while the
  transcode runs (players start at its first segment, not near its live edge) and static from
  just after it is done, every segment available, so dash.js plays to the end and seeks anywhere
  (from ffmpeg's own MPD a player that had started stopped short: at 42 s of a 3 min video). DASH
  sessions play at all: the router served none of their segments. A paused player keeps its
  session (reloading the playlist counts while the transcode runs), and a player that fetches
  nothing for a while (a background tab, a locked phone) keeps a running transcode for 10
  minutes. A finished transcode stays for 4 hours after its viewer's last fetch, so a long pause
  plays on; one cut short (the session reaped, ffmpeg failed) never ends the video there: its
  playlist answers 410 and the web player opens the video again where it was, as it also does
  when its stream is gone for good (404 after the retries), instead of the error screen, and
  marks a video watched only near its real end. A transcode opened at a position (a resume, such
  a reopen) starts there: ffmpeg seeks to it, and its playlist lists the part before as gaps, so
  the player's times, seek bar, progress and resume point stay the video's, and a seek before it
  opens the video again there (it started at 0 before, and the player jumped back). A session's
  folder is removed when its player closes it, and one a previous run left behind once it has not
  changed for 90 s; nothing removed those folders before. A transcode's segments take about 4.8
  GB an hour for the 1080p ladder on the cache's disk, outside the cache's cap: no transcode
  starts with less free there than `[media] transcode_min_free_gib` in server.toml (default: a
  tenth of the disk, between 3 and 20 GiB), and below it the transcodes kept for closed sessions
  go, the longest unwatched first. A refused video says "not enough disk space to prepare this
  video" (507), not that the server is busy.
- **Video transcodes give their slot back.** A transcode slot was taken at each session's open
  and never returned, so once a process had run as many transcodes as it has slots (a quarter of
  its CPUs by default), every 720p-and-up video played direct, whatever the player could decode,
  and anything smaller was refused. A slot now comes back when its transcode ends, when its
  session closes or is reaped, and when the open fails after taking it.
- **A live page's yt-dlp lookup runs once for the channels that share it.** A channel that found
  no URL cached just as the shared lookup ended started yt-dlp again for the same page; it now
  takes the URL that lookup cached. (CI saw it as the test's fake yt-dlp running twice and
  closing its channel twice.)
- **The cluster updates itself** (Epic 35, stories 35.4–35.7, 35.12, 35.13). In its quiet window
  (default 03:00–05:00) and with nobody watching TV, the home installs a new verified release
  of its channel, then every joined machine, one at a time, each watched before the next; a
  machine that was off is caught up when it comes back, and none runs ahead of the home.
  Before anything changes the home rehearses the release's migrations on a scratch copy of its
  database; at the switch it dumps the database (checked to read back), migrates it, and watches
  the new release for ten minutes. A release that does not stay healthy is rolled back on its
  own: the previous release runs again and, when the database had been migrated, the dump is put
  back first. A power cut at any step is finished at the next start. A plain server's update
  brings its pipeline's own Python environment too, with the extras the machine had. Headless
  and plain server installs do all
  of it by themselves; a desktop app that runs the server (Mac, Windows, Linux)
  downloads and checks the release the home orders and shows "Install Maktaba <version>" (on a
  machine: the cluster's update waits for it), and installs it when someone clicks there — the
  rollout goes on with the others meanwhile, and a newer release replaces an order nobody
  clicked. Admin → Updates shows each run's steps on the home and on every machine, the machines
  waiting for a click with their version, and Cancel, Retry, Skip, Roll back and Resume;
  automatic installs are now on by default (Pause turns them off). Releases carry their
  migrations (`maktaba-migrations-<version>.tar.gz`, listed in the signed `release.json`).
- **Every update failure says why.** Admin → Updates shows, for the home and each machine, its
  last update attempt: when, from which release to which, the step it stopped at, the reason in
  plain words (English and Arabic), the raw error, and what to do next (Retry, the command that
  fixes it, or the logs). It covers the desktop app's own updates as well as the cluster's
  runs: the app records each try (no network, a missing download, a signature that does not
  match, a folder that needs an administrator, a full disk, an app macOS runs from a temporary
  place or still marks as downloaded, an installer error, a relaunch that did not take) and
  says why in its menu and an OS notification, and keeps its own log (`app.log` beside
  `server.log`). A release that fails to start (the dump before its migrations, a migration, a
  database newer than it, the pipeline's environment) is recorded too, and the release before,
  back running, reads "0.3.0 failed to start: <reason>. The previous release runs again." The
  Mac app's setup screen no longer hides why it could not start the server (a login item
  macOS refuses for an app with no code signature says so, with the fix). An update counts as
  installed only once the server is back on the new release. Every failure also links to its
  logs: the logs page opens on that update's time window with the machine's own `server.log`,
  `app.log` and `launcher.log` beside the api's lines (a node's fetched over the machine
  channel), "Download the logs" bundles the same window with those files, and the desktop app's
  menu and notification open the logs page, or the folder with `app.log` and `server.log` when
  the server does not answer.
- **A machine follows its home's database by itself.** When the home's database moves (an
  app's move from an operator Postgres to its own, a new port, a restore) or a machine's role
  is missing on it, the machine says it cannot log in, the home makes its role again with a
  new password and hands it the database's address over the machine channel, and the machine
  starts again with it, within a minute, with no one doing anything. Until then the Updates
  and Machines pages say "this machine can't reach the home's database" and why, and a home's
  update waits for its machines to follow it before it commits (each for ten minutes at most).
  The home re-makes the access only where that mends the login (a refused password, a database
  that moved), with a growing pause between tries, never during a switch of the home or from
  a home that has not learned of one; the machine keeps its pinned CA, and no password shows in
  the database's logs (SCRAM verifiers) or in a failure's text.
- **The Apple-Silicon Mac app is released again.** Every release since the app was signed ad
  hoc (v0.3.0-rc.7, v0.3.1) failed its macos-arm64 build, so their update feeds had no
  Apple-Silicon entry and a Mac home could not update. Without the Apple secrets the release
  handed tauri empty notarization settings, which it took as a request to notarize ("Team ID
  must be at least 3 characters"). The Mac builds now get them only for a Developer ID app with
  a complete set, and CI's mac-app gate builds the way the release does, with those settings
  set but empty.
- **Every desktop app updates, and a Mac update says why when its server does not come back.**
  Apps that run no server (the Intel Mac app, an app connected to another Maktaba, the Windows
  portable) now show their update. After an app update the Mac registers the server's login item
  again and waits for the server on the new release before counting it installed; an ordered
  install checks, without prompting, whether macOS lets the app change itself (translocated,
  read-only, an administrator's, App Management, a locked bundle) and never reaches a password
  prompt. An app built locally says it cannot update itself (`local_build`: install the release
  by hand once) and blocks nothing. A Windows installer that does not finish is recorded and an
  unattended home's server comes back. One install at a time, with a download timeout and
  progress.
- **Cluster updates retry sensibly and can be unblocked.** A failure that may pass is retried at
  once, after 15 minutes, then in the next window (one attempt per window); one that needs a
  person starts no new runs and is told once. An admin can try a blocked release again or install
  it now. `catch_up` is honoured, a machine whose failure was mended is caught up, each node is
  drained for its own switch, only a verification or a rehearsal on the real data blocks a
  release, and a home whose window always has a viewer is told after three windows, with
  "Install now". Old dumps are pruned to `backups_keep`; a failed restore into an operator's own
  Postgres leaves it as it was.
- **One rule for which release is newer.** A release numbered higher but built before the one
  running (an rc tagged after its release) is passed by on every updater, instead of being
  offered, refused as a replay, or installed as a downgrade; a hotfix on an older minor still
  moves up. A release seen before its signed files were attached is verified once they are; a
  failed download is retried, never a verification failure that blocks the release.
- **Releases are guarded and never rewritten.** A tag at or below a published release, off
  `main` or without a passing CI run is refused; each release stays a draft until it is complete
  and becomes the latest only when it is the highest stable with every desktop platform in its
  verified feed; a re-run never rewrites a published release, and its files are reproducible.
  `install.sh` with systemd sets up the self-updating service; a .deb/.rpm upgrade restarts the
  service and leaves the migrations to the database step; the Sigstore trusted root is refreshed
  by a weekly PR.
- **No stale page after an update.** The service worker carries each build's stamp, pages load
  from the network first, and a page an older release left open reloads once instead of going
  blank.

## [0.2.0] - 2026-10-19

Closes the stub audit: features that were half built, settings that were
stored but never read and notices nobody saw are finished, removed or gated.
GraphQL covers every root field over the REST handlers, and the REST access
rules behind both were tightened. Schema revision 140 (migrations 0137-0140).

### Added

- **Maktaba.app runs its own language model** (Epic 36, story 36.11). The app bundles Ollama
  0.40.2 (pinned and checksum-verified in `make mac-app`) and the supervisor runs it as the
  `ollama` role on its own loopback port (11533, so a user's own Ollama keeps 11434), with its
  models in the app's `models/ollama` and its log in `~/Library/Logs/Maktaba/ollama.log`. Every
  LLM stage (check-tags, check-clips, enrich, display titles) uses it through
  `MAKTABA_OLLAMA_URL` unless Settings name another model server. First-run setup (step `llm`)
  and `maktaba-server models llm` download `qwen2.5:7b` with progress after showing the disk
  space it needs; the server downloads a missing model in the background, the menu bar shows
  it as ready or downloading, and `GET /llm` on the supervisor reports its health.
  `migrate-from-dev` carries the dev setup's `~/.ollama/models` over.
- **Find metadata and Re-enrich work** (#133). A video's metadata suggestions come from
  Maktaba's own intel (transcripts, chapters, the book a series explains, file and
  folder names, sibling episodes, tags, speakers) and, when configured, a web search
  (Google Programmable Search, or Ollama's web search; extension "Web search", with a
  real Test connection) that the language model reads. No outside catalogue. Every
  field carries its confidence and its sources in the review panel. The searches run
  as the engine's `enrich` stage (shown in the control centre; queued jobs no longer
  pile up), are cached, and stay within daily caps (`rules.enrich`). The model gets no
  tools, its answer is validated JSON, and page text is treated as untrusted data.
  Migration 0139.
- **GraphQL: every root field is live** (#134). The schema runs each field through
  the REST route that owns the operation, in process, with the same middleware
  (rate limits, idempotency, body limits, admin checks, visibility, audit). Nested
  video fields are batch-loaded, depth and cost limits apply
  (`MAKTABA_GRAPHQL_MAX_DEPTH`, `MAKTABA_GRAPHQL_MAX_COMPLEXITY`), `videos` and
  `jobs` are cursor connections, and subscriptions run over graphql-transport-ws.
- **GraphQL persisted queries** (#136): Apollo APQ, stored in
  `graphql_persisted_queries` (migration 0140), with an allowlist mode
  (`graphql.persisted_only`, default off).
- **YouTube search on the Search page through yt-dlp** (#131), no API key: results
  load after the local ones, each has an Import button that stages a `youtube`
  enrichment candidate, and the section hides itself when yt-dlp is missing.
- **Admin password reset** (#131): `POST /api/users/{id}/reset-password` with a
  one-time temporary password or an admin-set one. Every reset revokes the user's
  sessions and is audited; a must-change session sees only "Choose a new password"
  (migration 0138, `users.must_change_password`).
- **Notices for budget, simulcast cap, failover and staging crash loop** (#132): the
  80 % and 100 % crossings of the remote viewing budget and the simulcast cap, a
  failover to the hot standby and a staging slot crash loop go to notifications
  (migration 0137, `tv_slot_events`).
- **Web** (#130): Account > History (watch history and the activity timeline);
  "Revert to original" on the enrichment panel and `a`/`d` keys in the review queue;
  an optional `rules.enrich.auto_accept_threshold` (default 0 = off); an update badge
  on Settings, the Docker update hint up front and Markdown release notes; "Behind
  live" / "Return to live" in the Live TV player; Scan now shows its progress.

### Changed

- **Dead settings wired** (#132): `search.fts_weight`/`semantic_weight`,
  `recs.cache_ttl_sec`, `session.url_ttl_sec`, `pause.grace_sec`,
  `ws.heartbeat_interval_sec`, `max_lanes_per_daypart` (default 0 = no limit), the
  library `ignore_globs`, `sweep_interval_sec` (default 0 = off) and
  `stt.initial_prompt` are now read; `auth.open_registration` has a toggle on
  Admin > Users. Settings that could not work were removed (`subtitle.url_ttl_sec`).
  Defaults keep today's behaviour.
- **Filler edits go through the draft flow** (#132) when TV staging is on, and are
  committed by the hot swap with the rest of the change.
- **Backend gaps closed** (#128): Update now and the update scheduler say plainly that
  automatic installs arrive with the update orchestrator instead of recording failed
  runs; the relay overview counts servers and tunnels correctly; self-deleted accounts
  are erased with their push logs; the pipeline `doctor` really checks the database,
  schema, ffmpeg and directories; Windows disk-free and key-file locking work; push
  deliveries are recorded.
- If the self-update re-exec fails, the old binary is restored and the rollback is
  reported (#130).

### Removed

- The self-service "Forgot password?" flow and its reset-token store; unused tokens
  that were written to the log are deleted (#131).
- The no-op `POST /v1/account/email` cloud route and the empty root `maktaba` CLI
  package (#128).

### Security

- **REST access and audit** (#136): stream sessions answer only their owner or an
  admin (also on the streaming service's signed routes); collections, series, stats,
  tags, jobs, activity, item history, transcription summary, search suggestions,
  YouTube matches, recommendation rails, the control-centre reads and the WebSocket
  streams are scoped to the libraries the caller can read (404 otherwise); worker
  and extension details are admin-only. Library create, patch, scan and delete and
  video patch, delete, process and reprocess are audited; library scan is admin-only.
- **Must-change on full nodes** (#135): a session holding an admin-reset temporary
  password is held to the password form on full nodes too, and a native refresh is
  refused until the password is changed.
- **Thumbnail host allowlist** (#137): a staged YouTube thumbnail URL is parsed with
  `net/url` (https only, no userinfo or port, no backslashes) and must be on a YouTube
  image host (`*.ytimg.com`, `yt3.ggpht.com`, `yt3.googleusercontent.com`); crafted
  URLs such as `https://evil.example?x.ytimg.com/` no longer pass.
- **Dependencies with known vulnerabilities updated** (#140): grpc 1.83.2 and
  OpenTelemetry 1.45.0 (Go); react-router-dom v7, vite 6.4.4 and vitest 4.1.12 (web);
  Capacitor 6.2.2 (mobile); anyio, fsspec and setuptools (pipeline). The chromadb
  server advisories do not apply (Maktaba uses the embedded client only) and were
  dismissed as not used; the compose `chroma` service is bound to 127.0.0.1.

### Fixed

- **Plex finds the HDHomeRun tuner.** SSDP replies carry an absolute `LOCATION`
  (`http://<the address the client reaches>:8081/device.xml`) instead of `/device.xml`, answer
  `upnp:rootdevice` and the uuid as well, and the tuner multicasts `ssdp:alive` every five
  minutes and `ssdp:byebye` when it stops. The native HDHomeRun discover protocol (UDP 65001,
  libhdhomerun's packet format with CRC) is answered too, under the same tuner switch. The
  DeviceID now carries the HDHomeRun check digit (an existing id gets its last digit fixed,
  e.g. 87C7778E becomes 87C77788), and device.xml / discover.json report Silicondust like a
  real tuner. On the server itself the Live TV page shows the LAN address to type into Plex (#139).

## [0.1.0] - 2026-10-19

The first tagged release of Maktaba: a self-hosted library for lectures,
talks and series (video and audio) that understands what it holds and runs it
as live TV channels.

Highlights:

- **One server binary.** `maktaba-server` supervises the API, streaming and
  pipeline roles, serves the embedded web UI, advertises itself over mDNS and
  carries the installer CLI (`setup`, `setup --node`, `setup --self-managed`,
  `update`, `uninstall`, `models`). Per-platform archives for Windows (amd64),
  macOS (arm64, amd64) and Linux (amd64, arm64), `.deb`/`.rpm` packages, a
  Homebrew formula and container images.
- **Library intelligence.** Scanning and indexing, whole-library transcription
  on the queue engine (Whisper, with a second Cohere Arabic listen merged into
  Whisper's words), subtitles and search, chapters, topics and posters, series
  intelligence, tagging, production style and display titles; Plex index,
  season, topic and clip collections.
- **Maktaba TV.** Channels built from settings like real TV (a lineup of up to
  20 channels, show slots, fillers, no dead air), a guide and HLS/MPEG-TS
  output, Plex Live TV (HDHomeRun tuner + XMLTV DVR), TV slots with a hot
  standby, staged TV changes with automated verification, preview, hot swap
  and rollback, and remote viewing and simulcast through Cloudflare Stream.
- **Multi-machine homes.** LAN discovery and pairing, the sealed cluster
  channel, files on canonical share paths, worker nodes and full nodes that
  run the same server, sign-in across machines, node routing and the
  extension relay (nodes run the Plex stages without the Plex token).
- **Queue engine and control centre.** A change-driven multi-level job queue
  with per-job progress, the control centre and System Health pages, process
  supervision with crash recovery.
- **Signed releases and automatic updates.** Every release is checksummed
  and keyless-signed by this repository's release workflow (Sigstore);
  `release.json` carries versions, schema revisions and per-platform digests.
  Machines verify a release in process, offline, against a pinned workflow
  identity before installing it; side-by-side install layout run by
  `maktaba-launcher`, update policy and scheduling, rollback.
- **Clients.** The web app (system theme on every page), desktop (Tauri),
  mobile and TV (tvOS, Android TV) shells.
- **Platform.** SQLite or Postgres, migrations with a schema guard, secrets
  sealed on the home, rate limiting, audit log, observability, backup and
  integrity tooling.

Details of everything in this release:

### Added
- TV hot swap, push continuity and rollback (Epic 33 stories 33.7, 33.8,
  33.9): an approved TV change goes on the air in the front door, channel by
  channel at each one's programme boundary (`swap_wait`; a channel nobody
  watches at once; a rolling swap when the encoder budget is short): the HLS
  playlist continues on the new slot after one discontinuity, Plex/TS
  connections and the Cloudflare push are spliced at the keyframe both slots
  share without closing them (one push session, the same video, simulcast
  outputs untouched). The old live slot stays warm for `keep_warm_minutes`
  (15) as the rollback target. Roll back on the Changes page (and
  `GET/POST /api/tv/rollback`): warm (instant) while that slot is up, cold
  (the previous release staged again) within `rollback_retention_hours`; a
  crash of the new live slot in that window rolls back by itself. A live
  crash never puts an unapproved staged change on the air. Slot 0136
  `tv_swap_ops`, `tv_channel_switch`; pipeline stage `tv_rollback`.
- TV staging verification and preview (Epic 33 stories 33.5, 33.6): a staged TV
  change now runs on the staging slot (the third instance, started by the
  supervisor on the change's release binary and bound to its release); the front
  door warms the channels people watch, the probe channel and each changed
  channel in turn, within the encoders live and the hot standby leave, and
  samples the slot's HLS and live.ts every few seconds. The change controller
  writes every check into the change's report (slot health, first segment,
  cadence, wall clock and position, the programme on the air vs the new schedule
  and vs live, TS and HLS conformity, splice alignment with live — same PIDs,
  segment boundaries and PTS within a frame — encoder parameters vs live,
  Cloudflare ingest rules, push continuity, guide vs stream, encoder budget) and
  makes the change ready after `verify_soak_seconds`, or failed with the reasons;
  the staging slot stops when the change is cancelled, fails or goes live. The
  Changes page's Preview tab plays the staging channels for admins (the shared TV
  player, a live-vs-staging view, the staged guide strip; staged M3U and XMLTV for
  VLC), through `POST /api/tv/changes/{id}/preview` (a token for an hour) and the
  front door's `/stream/staging/…`; the web port lets only admins through. No
  staging HDHomeRun device. Compiled in by the `tvstaging` build tag, which
  the release and `make build-go` set for the streaming binary and image.
- TV slots and the hot standby (Epic 33 stories 33.1, 33.4): the channel
  engine runs in TV slot processes (`maktaba-streaming slot --slot
  blue|green|amber`, supervisor role `tv-slot`, loopback 8791-8793, probes
  9111-9113) behind the streaming role, which becomes the front door. The
  live slot serves the viewers; the hot standby runs the same channels warm
  (`rules.tv_staging.hot_standby`, on; `standby_warm`, `viewers` by default,
  within the encoder budget: live first) and takes over at once when the live
  slot crashes: HLS continues across one discontinuity, Plex/TS connections
  are spliced, the Cloudflare push keeps its session. Station output is
  splice-ready: wall-clock timestamps, keyframes on a wall-clock grid
  (`tv_output.keyint_seconds`, 2), fixed PIDs, aligned segments. Slot 0129
  `tv_slots`. `[tv] slots = "off"` in server.toml keeps the engine in the
  streaming process. Staging is compiled in by the `tvstaging` build tag
  (set by the release builds).
- Extension relay (story 31.R): machines run the Plex stages (index,
  season, topic and clip collections) again without the Plex token. Each
  Plex call goes over the machine's sealed cluster channel to the home,
  which checks the machine and the call against an allow-list, adds the
  token from `home_secrets`, calls Plex and passes the answer back with the
  token removed. Per-machine rate, concurrency, size and time limits;
  every call recorded in the hub's events file. Admin > Machines > Relay
  shows its state, the allow-list and the recent calls, and switches it
  (rules section `relay`, on by default; off, the Plex stages run on the
  home only). Cloudflare Stream stays home-only.
- Cloudflare Stream integration (Epic 32 story 32.6): a new
  "Cloudflare Stream" extension (account ID, API token, customer
  subdomain code) whose Test connection checks the token, Stream on the
  account, storage, Analytics (optional), creates the Stream signing key,
  learns the playback domain and checks that the home can push. The
  extensions page shows each check and the remote viewing switch (on only
  after a passed test from the last 24 hours) and can remove the
  integration (Maktaba's live inputs and signing key are deleted at
  Cloudflare). `/api/tv/remote` (status, test, enabled, remove).
  `maktaba-api cloudflare spike` runs the Stream spike against a real
  account (tools/tv/cf-spike.md).
- Secrets sealed on the home (slot 0120 `home_secrets`): AES-256-GCM
  under the key file `secrets.key` beside `server.toml` (or
  `MAKTABA_SECRETS_KEY`), never in the database and not readable by the
  machines' role. Every extension secret lives there: the Plex token is
  moved out of `extensions.settings` on the API's first start (once,
  idempotent), and the Plex stages now run on the home only.
- Process supervision (Story 22.9): `maktaba-server serve` restarts only
  the child process (api, streaming or pipeline) that crashed or hung,
  with exponential backoff, a crash-loop guard and `/healthz` liveness
  probes. It is configured by `[supervisor]` in `server.toml`.
  `restart = "never"` keeps the old behaviour, where any exit stops the
  whole stack. The streaming `/healthz` now fails when the live-channel
  engine is wedged, and the pipeline serves an event-loop `/healthz`. The
  state is shown at `GET /api/system/processes` (admin) and on the System
  Health page. Crashes are recorded in `health_samples`. A child's own
  ffmpeg and yt-dlp processes die with it: on POSIX through its process
  group, on Windows through a kill-on-close Job Object. Streaming also
  ties them to itself (Windows job, Linux `Pdeathsig`).
- Unified `maktaba-server` binary (`cmd/maktaba-server`): one executable
  that supervises the API, streaming, and pipeline roles, serves the
  embedded web UI (build tag `embed_web`), advertises `_maktaba._tcp`
  over mDNS, and carries the installer CLI — `setup` (first-run wizard),
  `serve --role`, `update`/`--check` (self-update), `uninstall`/`--purge`,
  `models` (Whisper model management), and `migrate`/`adduser`/`keys`
  (delegated to `maktaba-api`). Driven by a single `server.toml`; SQLite
  (`sqlite://`) and Postgres (`postgres://`) DSNs both supported. New
  `make server` / `make server-dev` targets.
- TV apps: tvOS (SwiftUI) and Android TV (Compose-TV) shells, GraphQL
  client stubs, pairing flow.
- Discovery: mDNS service advertisement, QR pairing, LAN probe.
- Subscriptions: signed-license entitlement layer + admin endpoints.
- Performance: canonical `shared/perf_budgets.yaml`, in-process LRU
  cache with admin flush endpoint, DB pool tuning.
- Scalability: shard, in-memory event bus, concurrency caps.
- Security: input validation, token-bucket rate limiter, SBOM surface,
  RFC 9116 disclosure metadata.
- Data integrity: atomic file writes, idempotency-key store,
  backup/restore planner, per-video integrity verifier.
- Observability: dashboard + alert manifests, canonical audit_log table.
- DevOps: release manifest, upgrade/rollback plan, multi-platform
  packaging skeletons.

### Changed
- `audit_log` table reconciled (slot 0054); supersedes earlier ad-hoc
  creations from Epics 9/12.

### Fixed
- Ports taken by another program (Splunk on 8088, a backup tool on 8080): `setup`,
  `setup --node`, `setup --self-managed` and `serve` check the web, API and
  streaming ports first and move a taken one to the first free port of 8780-8790,
  write it to server.toml (and `public_url` / `web_public_url` when they named the
  old port) and log it. A Windows full node prefers 8780 for the web UI. The web
  UI binds before serve goes on: a port it cannot bind stops serve with the reason
  instead of a log line. `serve --role X` never moves a port.
- Live TV in the browser with TV slots: the web port's stream gate now lets the
  stitched playlist's segments (`/stream/channel/{id}/s/{slot}/seg-N.ts`) through;
  before, every segment answered 404 and the player never started.
- TV staging: a staging start the supervisor did not take is retried on the
  change controller's next tick instead of being remembered as started (the change
  then failed as "not healthy"); the staging plan is read under the routing lock
  (a data race between the controller's plan updates and the warm-set sync).
