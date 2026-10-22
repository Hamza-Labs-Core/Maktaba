# Changelog

All notable changes to Maktaba are recorded here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The `[Unreleased]` section accrues entries between tagged releases.
The CI changelog gate (Story 22.5 AC-3) blocks user-visible PRs that
don't append a line here; docs-only PRs are exempt.

## [Unreleased]

### Added

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

- **The first video played right after Maktaba first starts is no longer refused.** The API makes
  the machine's stream signing key as it starts, and the streaming role looked for it every two
  seconds: a signed stream URL in between was refused ("bad signature"), and so was every one
  after the key file was replaced. A URL naming a key the streaming role has not read yet now
  reads the key file again at once (at most every 100 ms; a request that finds that read under
  way waits for it), and the key a replaced file held still verifies the URLs it signed, for a
  day.
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
