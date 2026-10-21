# Epic 38 — Maktaba for Windows: one installation that runs everything

> **Status:** spec. **Source:** `specs/epics/38-windows-app/`.
> **Anchors:** [Epic 36 — Maktaba.app](../36-mac-app/README.md) (the shape this follows: one app,
> the server payload inside, setup in the app, one updater), [Epic 35 — full machines and
> updates](../35-cluster-full-nodes-and-updates/README.md) (the launcher, `maktaba-server`
> supervision), Epic 37 — the node's copy of the home's database (`feat/node-replica`: `pgbin`,
> Postgres on Windows), `apps/desktop` (the Tauri 2 shell), `cmd/maktaba-server` (supervisor,
> `setup`, `ChildEnv`, paths), `cmd/maktaba-launcher` (app-bundle mode, the Windows logon task),
> `.github/workflows/desktop-release.yml` (the Windows NSIS/MSI client build).
> **Siblings:** Epic 39 is the Linux equivalent (another session). The shared pieces below are
> built once, here, for every platform.

## Goal

The owner's words (2026-10-20): *"also need the same one app setup on windows similar to mac"*.

1. **One Windows installation runs everything.** One installer gives the API, streaming (Live TV),
   the pipeline workers *including transcription*, semantic search, the web app, the database and
   ffmpeg. The server starts at sign-in, restarts anything that stops, and shows no console window.
   No Docker, no separate Postgres install, no hand-made `.cmd` loops, no terminal steps.
2. **It is the Maktaba desktop app.** Install, open, follow a short setup. The window is the web app;
   closing it leaves the server running; the tray icon shows its status.
3. **Postgres 16.15 is bundled**, private to Maktaba (loopback, port 54329), so it never meets another
   Postgres on the PC.
4. **Per user, in the user's session.** A LocalSystem service cannot see the NAS drives mapped in the
   user's session (`O:`, `Y:` → `\\Home-NAS\Originals`). The server runs as the signed-in user from a
   sign-in task, as Maktaba's own `setup --service` already does on Windows.

## Why (what exists on main, 1688a804)

| Piece | Windows today | This PC today (`E:\tv\maktaba-local`) |
|---|---|---|
| API, streaming, web | `maktaba-server.exe` + role binaries from a release zip, web embedded | `bin\` copied by hand from builds |
| Pipeline | not installed by the server; the node installer (`install-node.ps1`) builds a worker venv | the checkout's `pipeline\.venv` (`MAKTABA_PYTHON`) |
| Transcription | none: the transcribe stage is MLX-only (Apple Silicon) | none (left to the Mac) |
| Database | SQLite default, or a Postgres you bring | EDB Postgres 16 in `pgsql\`, `start-postgres.cmd` loop on 5432 |
| ffmpeg | whatever is on `PATH` | on `PATH` |
| Autostart | `setup --service`: a logon task running `maktaba-launcher --hide-console` (its console flashes, then hides) | task *"Maktaba node (maktaba-local)"*: `conhost --headless cmd /c start-maktaba-node.cmd`, a restart loop |
| Stopping | the supervisor kills every role on Windows (`terminationSignal` is `os.Kill`): no drain, the pipeline's claims wait out their lease, Postgres does crash recovery | the same |
| Paths | `%APPDATA%\Maktaba` (config, keys, `node.json`; roams with the profile), `%LOCALAPPDATA%\Maktaba\data` | `server.toml`, `stream-signing.key` beside the scripts, `node.json` in `%APPDATA%` |
| Desktop app | client only (NSIS/MSI + portable zip), no server inside | not used |

Epic 36 left gaps on main that every platform needs, the Mac included:

- `maktaba-server setup --json` has only the `status` step; `check`, `admin`, `libraries`, `models`,
  `plex`, `venv` and `finish` answer `unknown_step`, so the in-app setup cannot finish anywhere.
- Nothing does what [contract-update](../36-mac-app/contract-update.md) asks of the supervisor's next
  start (compare `payload.json`, back up and migrate, rebuild the venv only when the constraints changed).
- `venv_bootstrap` is POSIX-only (`fcntl`, `bin/python`).

## Shape

```
%LOCALAPPDATA%\Maktaba\                     the install (NSIS, current user, no admin rights)
  Maktaba.exe                               Tauri shell: window = web app, tray, setup screens,
                                            and `--server-agent` (what the sign-in task runs)
  uninstall.exe
  server\                                   the server payload (read-only, replaced by updates)
    bin\       maktaba-server.exe maktaba-api.exe maktaba-streaming.exe maktaba-launcher.exe
    postgres\  bin\{postgres,initdb,pg_ctl,pg_dump,pg_dumpall,pg_restore,psql,pg_isready,…}.exe
               + their DLLs, the MSVC runtime, lib\, share\                    (x86_64)
    ffmpeg\    ffmpeg.exe ffprobe.exe                                           (static, GPL)
    python\    python-build-standalone 3.12 (python.exe)                        (x86_64)
    pipeline\  maktaba_pipeline-<v>-py3-none-any.whl + constraints.txt
    licenses\  payload.json
  ── per-user state, beside the program (never touched by the installer) ──
  server.toml  node.json
  keys\        secrets.key stream-signing.key postgres passwords, HF/Plex tokens
  data\        the default [server] data_dir (supervisor/, node/)
  postgres\    the cluster          venv\     built on first run
  models\      chroma\    cache\streaming\    backups\    logs\    updates\   state\
```

Tauri's NSIS installer puts a current-user install in `%LOCALAPPDATA%\<productName>`, which is also
where the state lives. That is deliberate: one folder is Maktaba's home on this PC. The installer
writes and removes only its own files (`Maktaba.exe`, `uninstall.exe`, `server\`), so the state
beside them survives updates and an uninstall (uninstalling never deletes the library's database).
A user who installs to another folder still gets the state in `%LOCALAPPDATA%\Maktaba`.

- **The server runs from a sign-in task** (`\Maktaba Server`, this user, least privilege, 30 s after
  sign-in, restart on failure, no time limit): `Maktaba.exe --server-agent` starts
  `server\bin\maktaba-launcher.exe --app-bundle <payload> serve` in a console that has no window
  (`CREATE_NO_WINDOW`; verified on this PC: the launcher's CTRL_BREAK still reaches the server). The
  whole tree shares that console, so every role can be asked to stop cleanly. See
  [contract-agent.md](contract-agent.md).
- **`maktaba-server` supervises** postgres → api → streaming → pipeline (main worker) + N
  transcription workers, with the backoff, restarts and `/processes` it already has.
- **The venv is built on first run** from the bundled wheel and constraints with the bundled Python
  (the CUDA / torch wheels download then; the models too). Setup shows the progress.
- **x86_64 Windows 10 1809+ / 11.** Transcription uses CUDA on an NVIDIA GPU, else the CPU.

## Stories

| # | Story | Scope |
|---|---|---|
| 38.1 | **Windows paths** | `config.DirsFor("windows")` → everything under `%LOCALAPPDATA%\Maktaba` (`server.toml`, `keys\`, `data\`, `logs\`; `StateDir` = the root, as on a Mac). `node.json` moves there too (Go `NodeConfigPath` and the pipeline's `config_path`; the old `%APPDATA%` file is still read when the new one is absent). An install made before moves at the first command after the update (`MigrateWindowsDirs`, beside `MigrateMacDirs`), whether or not it has a `server.toml` (a `serve` runs without one): `server.toml` to `%LOCALAPPDATA%\Maktaba`; the keys (`secrets.key`, `stream-signing.key`, `postgres-*.password`) into `keys\`; `node.json`; and, while `data_dir` is the default, `models\`, `cache\`, `chroma\`, `postgres\` and `backups\` out of `data\` to beside it. With `MAKTABA_CONFIG` set, `server.toml` and the keys stay beside that file (the state folders still move); with `MAKTABA_NODE_CONFIG` set, `node.json` stays; the node installer's `node.env` files stay in `%APPDATA%\Maktaba`. Each piece moves on its own and only while its new place is empty (an empty folder counts): nothing is overwritten and no two folders are merged; a piece whose new place holds something else stays, and the command says which one Maktaba uses (a copy with the same bytes is a move that stopped before removing the old one, which it finishes). The move is drive-safe: `%APPDATA%` and `%LOCALAPPDATA%` may be on different drives (this PC: E:\ and C:\) or redirected to a share, so where a rename cannot cross, a file is copied beside its new name, flushed, compared, renamed into place and only then removed, and a folder moves file by file; a move cut short is finished by the next command and never loses a key. Any other failure (a file in use, no room) stops the command with the path, so a key that did not move is never replaced by a new one. Keys never roam with the profile again; the api and the pipeline run on their own read `keys\secrets.key`, or the old one until it has moved. The Mac is unchanged: its keys stay on the XDG path a developer stack reads (`tools/dev/stack.sh` sets neither `MAKTABA_SECRETS_KEY` nor `MAKTABA_CONFIG`), and its own move (Epic 36) gets the same cross-volume copy. `ChildEnv` gives the pipeline `PYTHONUTF8=1` / `PYTHONIOENCODING=utf-8` on Windows. |
| 38.2 | **Launcher in the Windows app** | `bundlePayload` recognises `<install>\server\bin` (beside `Maktaba.exe`, with `payload.json`); the state root is `%LOCALAPPDATA%\Maktaba`; a named stop event (`Local\Maktaba.Server.Stop`) ends `serve` cleanly from another process of the session; `--status` in app-bundle mode on Windows. No self-update (already refused in app-bundle mode). |
| 38.3 | **The shell runs the server on Windows** | Tauri: `--server-agent` on Windows (log `logs\server.log`, kill-on-close job, restarts the launcher with backoff, exits 0 on a clean stop); the sign-in task registered / removed / queried through `schtasks` (`/XML`, no localized output parsed); Pause / Resume / Restart / Stop through the stop event and `schtasks /Run`; `--server-start`, `--server-stop`, `--server-remove` for the installer hooks; every child process created without a window (`CREATE_NO_WINDOW`): setup steps, `schtasks`, Explorer. |
| 38.4 | **Tray on Windows** | the tray icon and its menu (status running / starting / problem / paused / stopped / update ready, Open Maktaba, Pause/Resume, Open logs, Close window vs Stop server and quit), Open logs in Explorer, the setup's external links through the shell's opener; a paused server stays paused across an app restart until Resume or the next sign-in. |
| 38.5 | **Every role stops cleanly on Windows** | the supervisor starts each child in its own process group and asks it to stop with CTRL_BREAK (Go roles: `os.Interrupt`; the pipeline: `SIGBREAK`, which it already handles), then kills after the grace; Postgres keeps Epic 37's `pg_ctl stop -m fast` hook. Without a console (a bare `serve` from a GUI), the old kill. |
| 38.6 | **Setup steps, all of them** *(shared: Mac, Windows, Linux)* | `maktaba-server setup --json`: `check` (platform, RAM, disk, GPU, what will run, warnings), `admin`, `libraries` (roots + NAS path map, Windows drive letters and UNC paths), `models`, `plex` (PIN flow), `venv` (relays the bootstrap's progress), `finish` (server.toml, database, migrations, admin seeded); pending state between steps; secrets on stdin only. The setup screens' wording follows the platform (`this Mac` / `this PC` / `this computer`). Every finding and refusal is a code with its values, which the screens (and Epic 39's terminal setup) say in English or Arabic; the step's English is a fallback, never shown beside them. Roots and both sides of the path map are whole paths. A stop asks the venv build and the models download to stop (SIGTERM; Windows: CTRL_BREAK to their own process group) before any kill. `maktaba-server setup` in a terminal runs these same steps (one implementation), asking only where the data goes and the database first. |
| 38.7 | **The pipeline's venv on Windows** | `venv_bootstrap`: `msvcrt` locking, `Scripts\python.exe`, default `%LOCALAPPDATA%\Maktaba\venv`, `SIGBREAK` as the stop signal; the supervisor builds the venv on first start when setup did not (progress in `/processes`); the CI gate builds a real venv from the release wheel on `windows-latest`. |
| 38.8 | **Transcription on Windows** | a CTranslate2 Whisper engine (faster-whisper's batched pipeline) behind the transcribe stage's transcriber interface: CUDA (`float16` / `int8_float16`) on an NVIDIA GPU, `int8` on the CPU; the `stt` extra gains its `win32` / `linux` markers (faster-whisper, ctranslate2, cuBLAS + cuDNN wheels); `PlanSTT` sizes the workers from the GPU's memory (`nvidia-smi`), else from RAM and cores; `models download` fetches the CTranslate2 turbo model on Windows. |
| 38.9 | **The second listen on Windows** | Cohere Transcribe Arabic through `transformers` on torch CUDA (the model is a `transformers` `cohere_asr` model), the CAMeL dictionary as on the Mac; a PC without CUDA or without the VRAM keeps Whisper's words and says why (as a Mac without the model does). |
| 38.10 | **Postgres in the Windows app** | builds on Epic 37's Windows Postgres (`pgbin`, `.exe` programs, `pg_ctl` fast stop, the Stop hook): `[postgres] enabled = "auto"` turns on inside any app payload; `postgres.exe` runs under a restricted token when the server's token is elevated (Postgres refuses an administrator, as `pg_ctl` does); TCP loopback only (no unix socket on Windows); the payload's MSVC runtime beside `postgres.exe`; nightly dumps as on the Mac. |
| 38.11 | **Build the payload and the installer** | `tools/release/win-app/` (`pins.env`, `payload.sh` for Git Bash, `check.sh`, `build-app.sh`): Go binaries (`embed_web`, windows/amd64), the pinned Postgres zip (= `pgbin`'s pin; a test keeps them equal), ffmpeg 8.1.2 (GyanD), python-build-standalone 3.12.15, the pipeline wheel + constraints, `payload.json`; the Tauri overlay (resources `server\`, NSIS current-user, installer hooks); `make win-app` builds it unsigned; CI gate `_win-app.yml` on `windows-latest`; `desktop-release.yml`'s Windows leg ships the server app. |
| 38.12 | **Updates** *(shared supervisor half)* | the supervisor's next start after an update (contract-update of Epic 36, built here for every platform): `payload.json` vs `state\payload.json` → `pg_dump` first, migrations, venv rebuild only when the constraints changed (wheel-only: reinstall the wheel), refuse a downgrade over a newer schema. Windows half: the shell stops the server (every file of the payload is in use while it runs), lets the Tauri updater run the NSIS installer, and starts the server from the new payload. See [contract-update.md](contract-update.md). |
| 38.13 | **Move an existing Windows install in** | the app finds a Maktaba this PC already runs (a sign-in task that runs `maktaba-server serve` directly, through Maktaba's own launcher, or from a script; the node installer's tasks) and setup offers to move it: stop it, carry its settings, keys, `node.json` and files (an SQLite database copied; a Postgres stays where it is, its url carried) into the app, start the app's server, verify, then remove the old task. The old folder is kept, and `maktaba-server adopt --undo` goes back. This PC's `E:\tv\maktaba-local` is the first case. See [move-an-install.md](move-an-install.md). |
| 38.14 | **Clean up** | README "Install on Windows" → the app; `docs/wiki/deployment.md` Windows section; `install-node.ps1` points a new machine at the app; `apps/desktop/README.md` Windows server mode. |

## Order and parallel tracks

- **Track A (server, Go):** 38.1 → 38.2 → 38.5 → 38.10 (38.5 and 38.10 build on Epic 37's Stop hook
  and Windows Postgres once those are on main).
- **Track B (pipeline, Python):** 38.7 → 38.8 → 38.9.
- **Track C (shell, Rust + web):** 38.3 → 38.4; 38.6 (Go `setup --json` + the web screens).
- **Track D (packaging):** 38.11 → 38.12 (the payload layout above is fixed now).
- **Then:** 38.13 on this PC (only with the owner's OK), 38.14.

Each story ships as its own PR into main with tests and green CI (merge commits, review first).

## What is shared with Epic 36 and Epic 39

| Piece | Built in | Used by |
|---|---|---|
| `setup --json` steps (38.6), platform wording of the setup screens | here | Mac, Windows, Linux |
| supervisor's next start after an update (38.12, supervisor half) | here | Mac, Windows, Linux |
| `venv_bootstrap` on Windows (38.7) | here | Windows |
| `pgbin`, Postgres `.exe` programs, `pg_ctl` fast stop, the supervisor's Stop hook | Epic 37 | Windows here, nodes |
| CUDA Whisper engine (38.8) | here | Windows, Linux with an NVIDIA GPU |

## Owner decisions still open

- **Code signing (Authenticode).** Without a certificate the installer and `Maktaba.exe` are
  unsigned: SmartScreen warns on the first download ("More info → Run anyway"), and some antivirus
  tools are stricter with unsigned programs that start at sign-in. A local build runs unsigned. An
  OV certificate (or Azure Trusted Signing) goes into `WINDOWS_CERTIFICATE` /
  `WINDOWS_CERTIFICATE_PASSWORD` (`desktop-release.yml`); the payload's own binaries are signed by
  the same step. Not blocking any story.
- **The Tauri updater key** (shared with the Mac): `plugins.updater.pubkey` is still a placeholder.
- **Data location for large libraries:** `%LOCALAPPDATA%\Maktaba` (on the system drive) by default;
  setup can offer another folder for `postgres\` and `models\` (a follow-up if wanted).
- **The ffmpeg licence (GPL v3):** as on the Mac, mirroring the source tarball as a release asset.
