# Epic 36 — Maktaba.app: one Mac installation that runs everything

> **Status:** spec. **Source:** `specs/epics/36-mac-app/`.
> **Anchors:** [Epic 35 — full machines and updates](../35-cluster-full-nodes-and-updates/README.md)
> (the launcher layout, `maktaba-server` supervision, self-update), [Epic 22 — DevOps](../22-devops/README.md)
> (process supervision 22.9, release pipeline 22.5), `apps/desktop` (the Tauri 2 shell),
> `cmd/maktaba-server` (supervisor, `setup`, `ChildEnv`), `deploy/packaging` (Homebrew formula,
> `install.sh`, the node installer), `tools/dev/stack.sh` (what the home Mac runs today).

## Goal

The owner's decisions (2026-10-20):

1. **One Mac installation runs everything.** A home Mac installs Maktaba once and gets the API,
   streaming (Live TV), the pipeline workers *including MLX transcription and the second listen*,
   semantic search, the web app, the database, ffmpeg, starting at login and restarting anything
   that stops. No Docker, no Homebrew Postgres, no terminal steps, no Claude-session-held processes.
2. **It is a Mac app: `Maktaba.app`.** Drag to Applications, open, follow a short setup. The window
   is the web app (as today); closing it leaves the server running; the menu-bar item shows status.
3. **Postgres is bundled** inside the app (≈30–40 MB for Apple Silicon), private to Maktaba, so it
   never clashes with another Postgres on the Mac.

## Why (what exists on main, 127c47be)

No single Mac path runs everything today (survey 2026-10-20):

| Piece | Released install (`brew` / `install.sh` / `maktaba-server setup`) | Home Mac today (`tools/dev/stack.sh`) |
|---|---|---|
| API, streaming, web | ✅ binaries, web embedded (`-tags embed_web`, :8088) | `go run` + a Vite dev server held by a Claude session |
| Pipeline (incl. transcription) | ❌ not installed — the supervisor's `python3 -m maktaba_pipeline` crash-loops | `uv run --all-extras` from a checkout |
| MLX / search extras, models | ❌ | manual |
| Database | SQLite default (no TV slots), or a Postgres you bring | Postgres in Docker |
| Autostart | only via `brew services` or a second `setup --self-managed` | none (nohup), nothing restarts a crash |
| Transcription settings | `[transcription]` → `MAKTABA_TRANSCRIPTION_*`, which the pipeline never reads (it reads `MAKTABA_STT_MODEL`) | env in the script |
| Paths | XDG (`~/.config`, `~/.local/share`) on macOS | `~/.maktaba` |

`apps/desktop` is a client only (no sidecar). The Mac Docker overlay (`docker-compose.mac.yml`) cannot
work as written (macOS ffmpeg into a Linux container, an unread `MAKTABA_STT_BACKEND`).

## Shape

```
Maktaba.app/Contents/
  MacOS/Maktaba                         Tauri shell (window = web app, menu bar, setup screens)
  Resources/server/                     the server payload, signed with the app
    bin/  maktaba-server maktaba-api maktaba-streaming maktaba-launcher
    postgres/  bin/{postgres,initdb,pg_ctl,pg_dump,pg_restore,psql} lib/ share/   (arm64)
    ffmpeg/   ffmpeg ffprobe                                                       (arm64, static)
    ollama/   ollama llama-server       the language model server, Ollama 0.40.2 (arm64)
    python/   python-build-standalone 3.12                                         (arm64)
    pipeline/ maktaba_pipeline-<v>.whl + constraints.txt (every extra pinned)
  Library/LaunchAgents/com.hamzalabs.maktaba.server.plist   registered with SMAppService
```

Per-user state, all under **`~/Library/Application Support/Maktaba/`** (`data/`, `postgres/` (cluster),
`venv/` (built on first run), `models/` (`models/ollama/`: the language model), `chroma/`, `keys/` (secrets, stream signing, cluster),
`cache/streaming/`, `backups/`), logs under **`~/Library/Logs/Maktaba/`**.

- **The server runs as a login item** (`SMAppService.agent`) — the bundled `maktaba-launcher serve`
  under launchd with `KeepAlive`. It runs whether or not the window is open, and starts at login.
- **`maktaba-server` supervises** postgres → api → streaming → pipeline (main worker) + N
  transcription workers + the index worker, and the language model server (`ollama`), with the
  backoff/restart and `/processes` it already has.
- **The search index has a worker of its own** (`pipeline/index`): the `index` and `chapter_infer`
  stages and the semantic search (gRPC 127.0.0.1:9093, which the API gets as
  `MAKTABA_PIPELINE_SEARCH_ADDR`), with `MAKTABA_PIPELINE_SHARE=off` so nothing in it waits on the
  NAS. The main worker, which reads the share and is killed when a stalled share hangs it, opens
  no Chroma (`MAKTABA_PIPELINE_SEMANTIC=off`), so it can no longer cut an index write in half;
  Chroma still has one process per directory. `[pipeline] index_worker = "off"` puts it back in
  the main worker.
- **The language model is local too.** The LLM stages (`check_tags`, `check_clips`, enrich,
  display titles) ask an Ollama server; on the home Mac nothing provided one and ~7,600 jobs failed
  with "connection refused". The app bundles Ollama (story 36.11) and runs it like every role.
- **The pipeline venv is built on first run** from the bundled wheel + constraints with the bundled
  Python (the MLX/torch wheels, ~1–2 GB, download then; models too). The app stays a reasonable
  download; setup shows the progress. Re-built on update only when the constraints change.
- **Apple Silicon only** for the home app (MLX). Intel Macs keep the Homebrew/node paths.

## Stories

| # | Story | Scope |
|---|---|---|
| 36.1 | **Bundled Postgres role** | supervisor role `postgres`: `initdb` on first run (scram, random password in `keys/`), `pg_ctl` start/stop with the supervisor, loopback-only on a private port (default 54329) + unix socket in the data dir, health check, `MAKTABA_DATABASE_URL` for every child, nightly `pg_dump` into `backups/` (keep 7), clean shutdown before exit. Postgres becomes the default home DB on macOS (TV slots on). |
| 36.2 | **Mac paths** | `config.Path()` / data dir / logs on macOS → `~/Library/Application Support/Maktaba` and `~/Library/Logs/Maktaba` (XDG kept on Linux; an existing XDG install is migrated once). Chroma dir, models dir, keys, streaming cache all set by `ChildEnv`. |
| 36.3 | **Pipeline in the bundle** | `MAKTABA_PYTHON` → bundled Python; first-run venv bootstrap (`python -m venv` + `pip install --constraint` from the wheel, extras `stt,search` on arm64), progress reported to the supervisor/UI, resumable, re-run when constraints change. |
| 36.4 | **Transcription the way the home runs it** | supervisor runs a main worker `--exclude-stages transcribe` + N transcribe-only workers (`MAKTABA_STT_SLOT`), N and the STT memory knobs sized from RAM (the 96 GB Mac's stack.sh values as the top tier); `[transcription]` wired to `MAKTABA_STT_MODEL` / `MAKTABA_STT_REFINE*`; `maktaba-server models` downloads exactly what the pipeline loads (turbo + Cohere second listen, or Cohere from the NAS share). |
| 36.5 | **Login item + menu bar** | Tauri shell: register/unregister the LaunchAgent via `SMAppService`; menu-bar status (running / starting / stopped / update ready) from the supervisor's `/processes`; Open Maktaba, Pause/Resume server, Open logs, Quit (window only) vs Stop server. |
| 36.6 | **First-run setup in the app** | screens: what will run + disk/RAM check → admin account → library folder(s) / NAS path map → models (Hugging Face sign-in for the gated Cohere model, or use the NAS copy) → optional Plex sign-in → done (opens the library). Backed by `maktaba-server setup --json` steps so the CLI and the app share one implementation. |
| 36.7 | **Build the payload** | CI (desktop-release, macOS arm64): build the Go binaries (`embed_web`), fetch pinned Postgres, ffmpeg and python-build-standalone with checksums, build the pipeline wheel + constraints, lay out `Resources/server`, codesign every Mach-O inside, notarize the `.dmg`. A local `make mac-app` does the same unsigned. |
| 36.8 | **Updates** | the Tauri updater replaces the whole app (shell + server payload); on next start the supervisor migrates the DB (backup first, as today), rebuilds the venv only if constraints changed, restarts the roles. The launcher's own self-update is off inside the app (one updater). |
| 36.9 | **Move the home Mac in** | `maktaba-server migrate-from-dev`: stop stack.sh services, `pg_dump` the Docker Postgres → restore into the bundled one, copy `~/.maktaba/{chroma,stream-signing.key,…}` and HF models, carry `MAKTABA_PATH_MAP` and the STT settings into `server.toml`, verify (library count, transcripts, Live TV host check-in), then retire Docker Postgres. Reversible (the Docker volume is kept until confirmed). |
| 36.10 | **Clean up** | drop the broken Mac compose overlay and stale plists/docs (`docs/wiki/deployment.md` §8–9, legacy `com.maktaba.*.plist`), README "Install on a Mac" → the app; keep `stack.sh` as the developer path only. |
| 36.11 | **The language model in the app** | see below. |
| 36.15 | **Where the data lives** | see below. |
| 36.16 | **Backup settings** | see below. |

## 36.11 — The language model in the app

**Payload.** `Resources/server/ollama/{ollama,llama-server}`: Ollama 0.40.2 (`pins.env`
`OLLAMA_*`, the release's `ollama-darwin.tgz`, SHA-256 from its `sha256sum.txt`), both thinned to
arm64. GGUF models such as `qwen2.5:7b` run in `llama-server` with Metal built in; the x86_64
CPU backends and the MLX engine (`mlx_metal_v*/`, ~400 MB, only for MLX-format models) are left
out. `pins-check.sh` is the dry run of the pins and the checked download (CI: the mac-app gate);
`check.sh` starts `ollama serve` from the payload and asks its API.

**Supervision.** `[llm]` in `server.toml` (`cmd/maktaba-server/internal/llmserver`): `enabled`
(auto: when the payload carries Ollama), `binary`, `port`, `url`, `models`, `pull`. `serve` runs
`ollama serve` as the `ollama` role with `OLLAMA_HOST=127.0.0.1:11533` (a set `port` is used as
is; the default moves to a free loopback port when another program holds it, so a user's own
Ollama keeps 11434), `OLLAMA_MODELS=<state>/models/ollama`, `OLLAMA_NOPRUNE=1` (a half-downloaded
model survives a restart), its output in `~/Library/Logs/Maktaba/ollama.log` (moved to `.1` past
50 MB at a start), probed at `/api/version`, restarted with the same backoff as the others.
`[llm] url` (an Ollama elsewhere) replaces the bundled one.

**The URL to the pipeline.** Every role gets `MAKTABA_OLLAMA_URL` (an operator's own is kept).
The pipeline reads it as the default of `rules.tagcheck.url`, the model server of check-tags,
check-clips, enrich (`rules.enrich.llm = "tagcheck"`) and display titles, and the cluster agent's
Ollama runtime already read it. A server set in Settings (a stored `rules.tagcheck.url`) wins.

**Models.** `[llm] models` (default `qwen2.5:7b`). The setup step `llm` (contract-setup.md) and
`maktaba-server models llm` report the disk space needed (the registry manifest) against the
space free and download with byte progress; Ollama resumes a partial download. The supervisor
checks every few minutes and downloads a missing model itself (`[llm] pull = "auto"`).
`migrate-from-dev` clones the dev setup's `~/.ollama/models` (or `OLLAMA_MODELS`) into
`models/ollama`, so the home Mac downloads nothing again.

**Health.** `/processes` has the `ollama` process and an `llm` object (up, ready, each model's
state and progress); `GET /llm` on the supervisor's admin port answers 200 when ready and 503
otherwise. The menu bar shows "Language model: ready" or "downloading qwen2.5:7b (42%)". The
queue-health dependency watcher (Epic 40, `dependency:model:tagcheck`) reads the model server
from `rules.tagcheck` like the stages do, so it probes the bundled server with no extra wiring.

## 36.15 — Where the data lives (another disk)

The large state (the bundled Postgres, its backups, the models, the caches, the venv, the data
folder) can live on another local disk, e.g. `/Volumes/Big/Maktaba`, chosen at setup or moved
later. It is `[server] data_dir`: for any data_dir the owner chose the state root is data_dir
itself (`Config.StateDir`); server.toml, the keys and the logs stay where they are.

**Setup.** A `storage` screen after `check` (contract-setup.md): the disks with their free space,
the folder on the suitable disk with the most room offered, a warning under 100 GB free. It writes
data_dir (and the marker, below) before anything is made in the state root, so nothing has to move
later; the CLI's terminal setup asks the same.

**The move.** `maktaba-server storage show` (each folder, its size, its disk's free space, the last
move, the old copies kept) and `maktaba-server storage move <dir> [--dry-run]` with Maktaba stopped
(it refuses while the supervisor answers, a launcher holds its lock or the cluster's postmaster
runs). Settings → Storage & discovery (admin only, `/api/server/storage*` proxying the supervisor's
`/config/storage*`) asks for the same move: the server writes `storage-move-request.json` beside
server.toml and starts again in-process; before anything runs, the next start does the move while
`/processes` says `storage_moving` (contract-update.md), then starts from the new place (a refused
or failed move is recorded in `storage-moved.json` and the install starts where it was).

**Acceptance criteria.**
1. A target is refused (nothing written) when it is relative, the current folder, inside it or
   around it, not empty, on a disk that is not local or whose file system Postgres cannot live on
   (FAT, exFAT, NTFS on a Mac, network shares, tmpfs), or without room for the copy plus a margin
   (5%, at least 1 GiB); a tight disk (under 20 GiB left) is a warning.
2. Every folder is copied (never renamed across volumes) under the paths move lock, files through a
   part file that takes its name once flushed, links made again; the copy is verified (every file
   with its size, no extra file) and the bundled Postgres must start on the copy before anything
   changes.
3. Then server.toml's data_dir is rewritten as text (comments kept), and the old folders are kept as
   `<folder>.moved-<stamp>`, never deleted (show and the page say how to remove them). `state/` is
   copied and left in place (the desktop app keeps files of its own there).
4. A move cut short (journal `storage-move.json`) resumes with the same target and mirrors again
   (files the server removed meanwhile are dropped from the copy); another target replaces a copy
   cut short (its partial copy is left, said as `partial_copy_left`) but is refused once server.toml
   may point at the first; until server.toml is switched the install runs from where it was.
5. Once chosen, the place is recorded (`storage.json` beside server.toml, a `.maktaba-storage`
   marker with the same id in the data folder). A start whose marker is missing (an external disk
   not mounted yet, an empty mount point) waits and says so (`storage_waiting`) and never makes a
   new database in its place; setup's `status` counts it as set up and `finish` refuses
   (`storage_missing_disk`).
6. The API enforces the admin role on every storage route (a role test); a move asked for is
   audited (`server` / `storage-move-requested`).

## 36.16 — Backup settings

The library database's dumps were fixed in code (backups/ beside the database, a nightly dump from
03:00, 7 kept, the pre-update dumps by `rules.updates.backups_keep`, `MakeRoom`'s 5% / 2 GiB
reserve and 1/5 share). They are `[backups]` in server.toml now (the server reads it before its
database step), every key unset keeping that behaviour: `dir`, `nightly`, `every_days`, `hour`,
`keep`, `update_keep`, `pre_update`, `reserve_percent`, `reserve_min_gb`, `max_share_percent`.

**Where they are changed.** Settings → Storage & discovery → **Backups of the library database**
(admin only: `GET/PUT /api/server/backups`, `DELETE /api/server/backups/dumps/{name}`, proxying the
supervisor's `/config/backups`; kept in `app_settings` too as `rules.backups`, `update_keep` as
`rules.updates.backups_keep`; audited `backups-changed`, `backup-deleted`), setup's `storage` step
(`backups_dir`, `nightly`, `hour`), and `maktaba-server backups show|set|delete`. Without a
launcher, the settings page's restart is serve starting again in-process.

**Acceptance criteria.**
1. Every value is range-checked (every_days 1–30, hour 0–23, keep 1–365, update_keep 0–50,
   reserve 0–50% and 0–1000 GB, share 1–100%); a new folder must be a whole path (checked like a
   storage target), local and suitable, not inside the database's folder, writable, with room.
2. A new folder while the old one holds dumps asks for a choice (422 `backups.move_dumps` with
   their count and size): moved (renamed, or copied and checked across disks, with the
   pre-update keep record) or left (no longer listed, pruned or restored from here).
3. The scheduled dump follows `nightly`, `every_days` and `hour`; the time of the last one is
   recorded apart (`<state>/state/last-nightly-backup`), so moving or deleting dumps never makes
   the next one due early. `keep` prunes the scheduled ones; `update_keep` the pre-update ones.
4. `pre_update = "off"` takes no dump before an update's migrations and logs that a failed
   migration then cannot be put back (the page and the command warn).
5. `MakeRoom` uses the reserve and share settings, the defaults unchanged.
6. The page lists the dumps (name, kind, size, date, disk) with the disk's free space; a dump is
   deleted one at a time, never the one an update under way may put back (409).
7. The API enforces the admin role on every backups route (a role test).

## Order and parallel tracks

- **Track A (server):** 36.1 → 36.2 → 36.4 (supervisor, Go, `cmd/maktaba-server`).
- **Track B (pipeline):** 36.3, then its half of 36.4 (pipeline settings).
- **Track C (app shell):** 36.5 → 36.6 (Tauri, `apps/desktop`; uses A's `/processes` and `setup --json`).
- **Track D (packaging):** 36.7 → 36.8 (CI + `make mac-app`; needs A–C's layout, starts on the fixed layout above).
- **Then:** 36.9 on the home Mac, 36.10.

Each story ships as its own PR with tests, through the usual flow (CI green, review, merge, move live).

## Owner decisions still open

- **Apple Developer ID** for signing/notarization (36.7): needed for a double-clickable download;
  without it the app runs from a local build (right-click → Open).
- **Data location** for large libraries: the defaults above, or a user-chosen folder for
  `postgres/` and `models/` (setup can offer it).
