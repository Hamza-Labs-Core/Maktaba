# Changelog

All notable changes to Maktaba are recorded here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The `[Unreleased]` section accrues entries between tagged releases.
The CI changelog gate (Story 22.5 AC-3) blocks user-visible PRs that
don't append a line here; docs-only PRs are exempt.

## [Unreleased]

### Added

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
- **The Linux app's downloads** (Epic 39, story 39.10). Each release builds the app as an
  AppImage, a `.deb` and an `.rpm` for x86_64 and aarch64, each with the whole server inside
  (its own Postgres, ffmpeg and Python), checked before anything is published; the `.deb` and
  `.rpm` stop the app's server when they are removed. For machines without a desktop the release
  carries the server payload (`maktaba-linux-payload-<version>-<arch>.tar.gz`) and `install.sh`
  among its assets, both in the signed `checksums.txt`: the notes say to verify the script and
  run it, no longer to pipe it from a branch.
- **The desktop app runs the server on Linux** (Epic 39, story 39.8). When setup finishes, the
  app copies its server into `~/.local/share/maktaba/runtime` and runs it as the user's systemd
  service, started at boot through linger; after an app update it moves the server to the new
  version at its next start (an app older than the server leaves it alone and says so). Pause,
  Resume and Stop server work from the tray, or from the window's Server menu where the desktop
  shows no tray; the tray starts at login when it can be shown. The tray, the Server menu and the
  app's dialogs follow the session's language (English or Arabic).

### Changed

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
  it, keeping its ACL; the pipeline is its one writer. A PC it does not belong to skips it: it
  is no home's machine, and its own join writes `%LOCALAPPDATA%\Maktaba\node.json`, never the
  other PC's identity. `maktaba-server` says so once at its start, and the app's setup check
  shows it (`node_config_other_pc`).
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
