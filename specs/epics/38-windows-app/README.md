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

- **The server runs from a sign-in task** (`\Maktaba Server <SID>`: one per user, least privilege,
  30 s after sign-in, a watchdog every 5 minutes that starts an agent that died, no time limit):
  `Maktaba.exe --server-agent` starts
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
| 38.1 | **Windows paths** | `config.DirsFor("windows")` → everything under `%LOCALAPPDATA%\Maktaba` for a new install (`server.toml`, `keys\`, `data\`, `logs\`, `node.json`; `StateDir` = the root, as on a Mac): a new install puts nothing in the roaming profile. **node.json: one per machine, never copied or moved, and the PC's it belongs to.** Every reader (Go `NodeConfigPath`, streaming, the pipeline's `config_path`, the node installer's `Get-NodeConfigPath`) takes `%APPDATA%\Maktaba\node.json` (a machine that joined before Epic 38) while it is this PC's (its `machine_id` is this PC's MachineGuid, `HKLM\SOFTWARE\Microsoft\Cryptography`) or no PC's yet, else `%LOCALAPPDATA%\Maktaba\node.json`, where a new join writes. The pipeline is the one writer of a node config (`cluster/config.py` `_write`): a join records `machine_id`, and the first new pipeline that loads a roaming config with none claims it for its PC (the field written in, every other key kept, the file replaced with ReplaceFileW so its ACL stays); maktaba-server, streaming and the scripts only read. A roaming config another PC claimed is skipped: this PC is no home's machine, its own join writes the local file and never the other PC's identity, `serve` says so once at its start (`config.ForeignNodeConfig`), and setup's `check` shows it (warning `node_config_other_pc`, `path`, `owner`; the words point to Help: Machines, Add a machine). A config no PC claimed, or a PC whose id cannot be read, always runs: no working node is taken down on a guess. The pipeline's `save()` (a re-join) writes the file it resolved, so a re-join or a hand edit is seen by every reader; to forget the home, every `node.json` that is this PC's goes, with what a write left beside it (the node's uninstall script names them, never another PC's; `join --rejoin` keeps the file as `node.json.bak` and removes the rest). A write that cannot finish (ReplaceFileW failing half way, and putting either file back failing too) leaves the old file under a backup name and the new one as a temp file (`.node.json-<time>-<pid>-<hex>.bak` / `.tmp`): while `node.json` is missing or broken, every reader takes the newest whole backup, else the newest whole temp file, as the node config (`_source`, `nodeConfigFile`, `Get-NodeConfigSource`), so a roaming config left that way is still this PC's and the node stays joined; the pipeline's next load puts it back with a rename that never replaces a file (a write that landed meanwhile keeps its own), and leaves a write's files alone for a minute from the time in their names. The temp file is created the user's alone (CreateFileW with the user-only DACL, no sharing; mode 0600 on POSIX), and when it cannot be, the credential is not written (a claim then fails as any failed claim). **Inside the app's payload nothing is copied or moved by itself** (`config.InAppPayload`: `MAKTABA_APP_BUNDLE`, or `payload.json` above the running executable's `bin`, links resolved; on a Mac too): the app's own files are in these paths from the start, and an install from before comes into the app only through 38.13's move, which keeps the old one for going back and leaves `node.json` where it is (the app and a restored old install share the one identity). **A release install outside the app gets copies of its config and keys, and keeps the originals**: at the first command after the update (`MigrateWindowsDirs`), whether or not it has a `server.toml` (a `serve` runs without one), `server.toml` is copied to `%LOCALAPPDATA%\Maktaba` and the keys (`secrets.key`, `stream-signing.key`, `postgres-*.password`) into `keys\`, each key and password limited to the user (a protected DACL with the user's SID only, as `node.json`'s `icacls /inheritance:r`). The originals in `%APPDATA%\Maktaba` are never removed by a command: an older version of the same install, a node's pipeline run from a checkout and the node installer still read them (the rule 38.13 follows); they roam with a roaming profile, and `uninstall --purge` removes them with their copies (not `node.json`, the machine's identity, which uninstall keeps on every OS). The copies win: this server reads its config and keys from the new place, and the api and the pipeline read the new key file before the old one. With `MAKTABA_CONFIG` set, `server.toml` and the keys stay beside that file; the node installer's `node.env` files stay in `%APPDATA%\Maktaba`. Each file is copied on its own and only while its new place is empty: a copy is never replaced (it is put at its name with MoveFileEx without `MOVEFILE_REPLACE_EXISTING`, a hard link elsewhere), and one that differs from its original (an older version rewrote it since) stays, and the command says once which one Maktaba uses (again only when that changes; `paths-move.json`). A command that finds every copy made changes nothing and takes no lock. The copy is drive-safe: `%APPDATA%` and `%LOCALAPPDATA%` may be on different drives (this PC: E:\ and C:\) or redirected to a share; a file is copied beside its new name, flushed and compared before it gets its name, so a copy cut short is made again by the next command. One command at a time copies: it holds `%LOCALAPPDATA%\Maktaba\paths-move.lock` (LockFileEx; flock for the Mac's move), taken only when there is something to do; a second command waits for it up to 30 s and goes on with the result, and past that stops and says why. Any other failure (no room, no access) stops the command with the path, so a key that was not copied is never replaced by a new one. **The large state is neither moved nor copied**: an install from before keeps `models\`, `cache\`, `chroma\`, `postgres\` and `backups\` in its default `data\`. Outside the app, the state root is decided once, at a command's start once `data\` exists: `data\` when it holds one of them, else the root beside it, recorded in `%LOCALAPPDATA%\Maktaba\state-root.json` (written once, never replaced); every later resolution reads the record (reading never writes), so a folder an older version makes in `data\` afterwards never moves the state root (and the bundled Postgres never starts an empty cluster beside the real one). A running Postgres's folder is never touched. `Config.StateDirOutsideApp` is that same rule and record for 38.13's move, which runs inside the app and reads an install from before by its own rule: the record, or the folders while that install has none; it never writes. The app's own state root is always the root, and inside the app nothing is recorded. The api and the pipeline run on their own read `keys\secrets.key`, or the old one until it has been copied. The Mac is unchanged: its keys stay on the XDG path a developer stack reads (`tools/dev/stack.sh` sets neither `MAKTABA_SECRETS_KEY` nor `MAKTABA_CONFIG`), and its own move (Epic 36, `MigrateMacDirs`, which moves) gets the same cross-volume copy, lock and app-payload rule. `ChildEnv` gives the pipeline `PYTHONUTF8=1` / `PYTHONIOENCODING=utf-8` on Windows. |
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
| 38.12 | **Updates** *(shared supervisor half)* | the supervisor's next start after an update (contract-update of Epic 36, built here for every platform): one database step before every role but Postgres: the record `state\database.json` (the release whose migrations the database holds) → refuse an older release for every process, else `pg_dump` first (the newest `[postgres] update_dumps` kept), the migrations, and on a failed migration the dump restored and every process stopped saying so; the API itself refuses a database ahead of it (`migrate up` exit 3, `serve`); the venv rebuilt in place only when the constraints or Python's minor changed (wheel-only: reinstall the wheel). Windows half: the shell stops the server (every file of the payload is in use while it runs), lets the Tauri updater run the NSIS installer, and starts the server from the new payload. See [contract-update.md](contract-update.md). |
| 38.13 | **Move an existing Windows install in** | the app finds a Maktaba this PC already runs (a sign-in task that runs `maktaba-server serve` directly, through Maktaba's own launcher, or from a script; the node installer's tasks) and setup offers to move it: stop it, carry its settings, keys and files (an SQLite database copied; a Postgres stays where it is, its url carried; a node's `node.json` stays where it is, the machine's one, which the app reads: it is never copied) and the firewall rules of its programs into the app, start the app's server, verify (it listens where other devices reached the old install, and the rules are in place), then remove the old task. The old folder is kept, and `maktaba-server adopt --undo` goes back. This PC's `E:\tv\maktaba-local` is the first case. See [move-an-install.md](move-an-install.md). |
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
- **The Tauri updater key** (shared with the Mac) is set up: the repository holds the signing
  secret and the `TAURI_UPDATER_PUBKEY` variable, which `desktop-release.yml` stamps into
  `tauri.conf.json` at build time (the repository keeps the placeholder). The app updates itself
  through signed updates from the first tagged release that includes it.
- **Data location for large libraries:** `%LOCALAPPDATA%\Maktaba` (on the system drive) by default;
  setup can offer another folder for `postgres\` and `models\` (a follow-up if wanted).
- **The ffmpeg licence (GPL v3):** as on the Mac, mirroring the source tarball as a release asset.
