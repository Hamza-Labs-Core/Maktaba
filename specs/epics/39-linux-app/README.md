# Epic 39 — Maktaba for Linux: one installation that runs everything

> **Status:** spec. **Source:** `specs/epics/39-linux-app/`.
> **Anchors:** [Epic 36 — Maktaba.app](../36-mac-app/README.md) (the same app on a Mac: payload,
> `setup --json`, the update contract), [Epic 35 — full machines and updates](../35-cluster-full-nodes-and-updates/README.md)
> (the launcher, verified releases, `rules.updates`), Epic 37 — the node's copy of the home's
> database (`feat/node-replica`: `pgbin`), **Epic 38 — the Windows app**, which builds the pieces
> every platform shares (the setup steps, the next start after an update, the transcription engine
> off Apple Silicon). This epic uses those pieces and adds what is Linux's own.

## Goal

The owner's decision (2026-10-20): *"also need the same one app setup on windows similar to mac … and
another for linux"*.

1. **One Linux installation runs everything**: the API, streaming (Live TV), the pipeline workers
   *including transcription and the second listen*, semantic search, the web app, the database and
   ffmpeg. It starts at boot and restarts anything that stops. No Docker, no distro Postgres, no
   terminal steps.
2. **It is a desktop app where there is a desktop**: install the AppImage (or the .deb / .rpm), open
   it and follow the same short setup as on the Mac. The window is the web app. Closing it leaves the
   server running, and the tray icon shows the server's status.
3. **The same payload runs a machine without a desktop** (a NAS, a mini PC, a VM): unpack it, then
   `maktaba-server setup` asks the same questions in the terminal. No window is needed.
4. **Postgres is bundled** (Postgres 16.15 for x86_64 and aarch64), private to Maktaba, so it never
   clashes with a distro Postgres.

## Why (what exists on main, 1159ba47)

| Piece | Released Linux install (`install.sh` / nfpm .deb / node installer) | What Maktaba.app does |
|---|---|---|
| API, streaming, web | ✅ binaries; system unit as user `maktaba` (nfpm) | the same binaries in the payload |
| Pipeline | node installer only: a venv from the wheel; the home install has none | venv built on first run from the payload |
| Transcription | ❌ the engine's `transcribe` stage is MLX only (`engine/stages/transcribe.py:55`), and the `stt` extra installs nothing off Apple Silicon (Epic 38.8 adds the engine for Windows and Linux) | MLX turbo + the Cohere second listen |
| Database | SQLite by default, or a Postgres you bring | bundled Postgres 16.15 (36.1) |
| ffmpeg / Python | distro packages (`Depends: ffmpeg, python3`) | bundled, pinned |
| Autostart | system unit (root to install) or the node's user unit + linger | login item |
| Setup | `maktaba-server setup` (CLI wizard, SQLite-oriented) | screens over `setup --json` (only `status` exists on main; Epic 38.6 adds the rest) |
| Updates | `maktaba-server update` / Epic 35 rollouts (Go binaries only) | the app updater (the next-start contract is Epic 38.12) |

`apps/desktop` already builds a Linux .deb and AppImage (`desktop-release.yml`), but only as a client:
no payload, no service, and macOS-only code in `agent.rs` (SMAppService, launchctl, `/usr/bin/open`).

## Shape

```
Maktaba_<v>_amd64.AppImage  ·  maktaba_<v>_amd64.deb  ·  maktaba-<v>-1.x86_64.rpm      (and aarch64)
  usr/bin/Maktaba                 the Tauri shell: window = web app, tray, setup screens
  usr/share/maktaba/server/       the server payload, the same tree as Maktaba.app's Resources/server
                                  (not among the resources in usr/lib: linuxdeploy rewrites ELF files there)
    bin/       maktaba-server (web embedded) maktaba-api maktaba-streaming maktaba-launcher
    postgres/  bin/ lib/ share/   theseus-rs 16.15.0 <arch>-unknown-linux-gnu, plus the libraries it
                                  links that a minimal system can lack (OpenSSL 3, libxml2, lz4, zstd,
                                  krb5), with an $ORIGIN rpath
    ffmpeg/    ffmpeg ffprobe     ffmpeg.martin-riedl.de 8.1.2 (the Mac's source), linux amd64/arm64
    python/    CPython 3.12.15    python-build-standalone <arch>-unknown-linux-gnu
    pipeline/  maktaba_pipeline-<v>-py3-none-any.whl  constraints.txt
    licenses/
    payload.json  versions, sources, pins, min_glibc; files: {manifest, count, bytes, sha256}
    files.sha256  every file (mode, size, SHA-256) and symlink, sorted: the manifest payload.json
                  names by its SHA-256 (new for Linux; the runtime copy checks every file against it)

maktaba-linux-payload-<v>-<arch>.tar.gz    the same server/ tree, for a machine without a desktop
```

Per-user state, XDG throughout (`config.DirsFor` already resolves these on Linux):

| Where | What |
|---|---|
| `~/.config/maktaba/` | `server.toml`, keys (`secrets.key`, `stream-signing.key`, `postgres-*.password`) |
| `~/.local/share/maktaba/` (data_dir, `0700`) | `postgres/` (cluster), `venv/`, `models/`, `chroma/`, `backups/`, `runtime/` (below), `state/`, and the launcher's `updates/` (its lock and `launcher.log`) |
| `~/.cache/maktaba/` | the streaming cache (`MAKTABA_CACHE_ROOT`), pip's cache for the venv build (a headless update downloads into `runtime/.download-*`, on the runtime's disk) |
| `~/.local/state/maktaba/logs/` (`0700`) | `server.log` (the launcher's and the server's output, rotated at 50 MB), `venv.log` |
| `~/.config/systemd/user/maktaba-server.service` | the server's unit; linger keeps it running at boot without a login |
| `~/.config/autostart/maktaba.desktop` | the tray at login (desktop installs only) |

- **The server runs from a per-user copy of the payload, `runtime/<v>/`** (story 39.3), never from
  the AppImage or `/usr/lib`. An AppImage is mounted at a new `/tmp/.mount_*` path on every run and only
  while it runs. A package upgrade replaces files under a running server, and a Python worker would
  then start against a venv built for the old payload. The copy makes updates inert until the server
  is stopped: install `runtime/<new>`, stop the unit, point `runtime/current` at the new version,
  start it. The one that ran before is kept as `runtime/previous`: a rollback goes back to it, and
  its Postgres can `pg_upgrade` a future major bump, which Maktaba.app cannot do. A version older
  than the newest this machine ran is refused ([below](#downgrades-refused-unless-asked-for)). See
  [contract-runtime.md](contract-runtime.md).
- **The server is a systemd user service** (`maktaba-server.service`, story 39.5). It runs
  `runtime/current/bin/maktaba-launcher serve` in app-bundle mode, with `Restart=always`,
  `KillMode=mixed` and `TimeoutStopSec=90` (the launcher's 60 s stop grace). Linger
  (`loginctl enable-linger`) starts it at boot. Pause and Resume are `systemctl --user stop|start`.
- **`maktaba-server` supervises** postgres → api → streaming → pipeline (main worker) + N
  transcription workers, as on the Mac.
- **The venv is built on first run** from the payload (`venv_bootstrap`, already POSIX) with
  progress in setup. It is built against `runtime/current/python` (the unresolved path), so a payload
  update with the same Python minor version needs no rebuild.
- **Supported systems:** x86_64 and aarch64, glibc ≥ 2.35, with a systemd user manager: Ubuntu 22.04+,
  Debian 12+, Fedora 36+, RHEL/Alma/Rocky 10, Linux Mint 21+, Pop!_OS 22.04+. The baseline is set by
  the ffmpeg and Tauri builds (glibc 2.35, libstdc++ of GCC 12). The bundled Postgres needs 2.34.
  `check` refuses lower (story 39.6). The desktop packages' tray library
  (`libayatana-appindicator`) comes from EPEL on RHEL/Alma/Rocky 10 (tauri makes it a hard
  dependency, and the shell needs it to start); a machine without a desktop needs nothing extra.

## Decisions

### Packaging and updates: the Tauri updater, AppImage first; no apt/dnf repository

- **AppImage** is the main download: per-user, no root to install or update, and one file for every
  distro on the baseline. **.deb and .rpm** come from the same `tauri build` for people who want
  package-manager integration (menu entry, `apt remove`). They carry the same payload and declare
  their libraries (`webkit2gtk-4.1`, `libayatana-appindicator3`) as dependencies.
- **One updater, the app's**, as on the Mac and Windows. tauri-plugin-updater 2.10+ (we lock 2.14.0)
  updates all three Linux formats: it replaces the AppImage in place, and installs a .deb/.rpm through
  the system's admin prompt. The feed is the same `latest.json` on the GitHub release, signed with
  the same updater key.
- **Not an apt/dnf repository.** It would be a second updater with a second trust root (a GPG repo
  key to create, host, rotate and publish). It would need hosting for the repository metadata, and
  root for every update. Updates would also land whenever the user runs `apt upgrade`, not when
  Maktaba is ready for them. With the runtime copy, a package manager's upgrade is harmless anyway:
  the new payload only runs once the shell activates it, in the order the update contract needs
  (backup, migrate, venv).
- **Headless machines** have no shell to run the Tauri updater. They update through Epic 35's
  verified-release path (Sigstore-signed `checksums.txt`, the watermark, `rules.updates`' quiet
  window and rollout). The artifact is the payload tarball, installed as a new runtime: `maktaba-server
  update`, or Update now in the web app, which runs it as its own transient user unit
  ([headless.md](headless.md#updates)).

### Downgrades: refused, unless asked for

An older app can start after a newer one (an old AppImage file opened again, a package downgraded),
and the payload it brings is older than the runtime that runs. Its server would meet a database the
newer one migrated. So the runtime keeps a **watermark**, the newest version it ever ran, and
`runtime install` / `activate` refuse anything older with `downgrade` unless `--allow-downgrade`.
`runtime status` says `update_pending` only for a newer payload (or the same version built again)
and `downgrade` for an older one: the shell keeps the running server and tells the user their app is
older than the server. Going back on purpose is a rollback to `previous` (`runtime activate previous
--allow-downgrade`, `maktaba-server update --rollback`), which does not lower the watermark. This is
Epic 35's rule for every other install.

### A system-wide `maktaba-server.service`: refused, never taken over

The nfpm package (and `install.sh` run as root) installs a system unit of the same name, run as the
user `maktaba`. The app's user unit would be a second server on the same ports with its own
database. `service install` refuses with `service_taken` even with `--replace` (a user does not stop
or replace a root-owned service), naming the unit and how its owner removes it. `status` reports it
as `system_unit`, and setup's `check` stops on it before anything is installed. Bringing that
server's data into the app is a migration of its own, outside this epic.

### Transcription: Epic 38's engine

Linux uses the engine Epic 38 builds for every machine off Apple Silicon: Whisper on CTranslate2 (38.8;
CUDA on an NVIDIA GPU, `int8` on the CPU) and the Cohere second listen through transformers (38.9).
What Linux adds is in [contract-stt-linux.md](contract-stt-linux.md): NVIDIA detection through
`/proc/driver/nvidia`, the pip CUDA libraries on the loader's path, aarch64 as CPU, and CI on both
architectures.

## Stories

| # | Story | Scope |
|---|---|---|
| 39.1 | **This spec** | this README, [contract-runtime.md](contract-runtime.md), [contract-stt-linux.md](contract-stt-linux.md), [headless.md](headless.md) |
| 39.2 | **The Linux payload** | `tools/release/linux-app/` (beside `mac-app/` and Epic 38's `win-app/`): `pins.env` per architecture (the Postgres URLs and SHA-256s are the ones `pgbin` pins; once `pgbin` is on main (Epic 37, PR #196), the test that keeps the Mac's pins equal to it covers these too), `payload.sh` (Go binaries for linux/amd64 or arm64, Postgres plus its library closure from the build host with an `$ORIGIN` rpath, ffmpeg, Python, the wheel and constraints; `files.sha256` with every file's mode, size and SHA-256 and every symlink, and payload.json naming it by its SHA-256, with `min_glibc`), and `check.sh`. `check.sh` scans every ELF for its newest GLIBC symbol, runs `initdb`, starts and queries a cluster, and runs `ffprobe`, the bundled Python and `venv_bootstrap --check`. `make linux-app-payload`. CI `_linux-app.yml` on ubuntu-22.04 and ubuntu-22.04-arm builds the payload and runs `check.sh` inside `ubuntu:22.04`, `debian:12-slim` and `fedora:40` |
| 39.3 | **Runtime copy** | `maktaba-server runtime install --from <payload> \| activate \| status \| prune` ([contract-runtime.md](contract-runtime.md)): copy into `runtime/.staging-*`, verify against `files.sha256`, rename; the first runtime becomes `current`; flip `current` while the unit is stopped, record `previous`, refuse a downgrade below the watermark, prune the rest, all under a lock. On top of 38.2's payload detection in the launcher: `MAKTABA_APP_BUNDLE` is the unresolved `runtime/current`, the state root is `$XDG_DATA_HOME/maktaba`, and `owner` decides whether the app or the machine updates it: with `headless`, `maktaba-server update` installs the release's verified payload tarball as a new runtime, and the API's Update now runs it as a transient user unit |
| 39.4 | **Linux paths in app-bundle mode** | `MAKTABA_CACHE_ROOT` and pip's cache under `$XDG_CACHE_HOME/maktaba`; the venv's log under the logs dir. The bundled Postgres's `enabled = "auto"` inside any payload is 38.10's |
| 39.5 | **The systemd user service** | `internal/service`: the app's user unit (the `runtime/current` launcher, `--log-file`), and `maktaba-server service install \| uninstall \| status \| start \| stop \| restart \| linger` (`--json`). The shell and the headless setup both call it, so neither writes units itself. Linger: polkit allows it without a password from an active desktop session; over SSH, `loginctl` asks in the terminal. `status` reports a missing user manager (no systemd as PID 1, e.g. WSL) as `unsupported` with the reason |
| 39.6 | **Setup on Linux** | the Linux part of 38.6's `check` (architecture, glibc ≥ 2.35, the systemd user manager and linger, a self-managed install that owns the unit already, disk, RAM). The `venv` step installs the runtime first (`stage: runtime`), and `finish` installs the service. `maktaba-server setup` on a machine with a payload and no window runs the same steps in the terminal ([headless.md](headless.md)), with `--answers` for automation |
| 39.7 | **Transcription on Linux** | per [contract-stt-linux.md](contract-stt-linux.md), on 38.8/38.9: NVIDIA detection on Linux, the driver checks, `LD_LIBRARY_PATH` to the venv's CUDA wheels, `CUDA_VISIBLE_DEVICES` per worker, aarch64 as CPU, and the Linux CI of the engine |
| 39.8 | **The desktop shell on Linux** | `apps/desktop`: the payload at `<prefix>/share/maktaba/server` (`/usr` in a package, `$APPDIR/usr` in the AppImage; mapped by each bundle's `files`, since linuxdeploy rewrites the ELF files under `usr/lib`), a linux server-service module (beside macOS's and Windows') that drives `maktaba-server service`, and a runtime install on start when the bundled payload differs from `runtime/current`. The tray uses StatusNotifier/AppIndicator (libayatana-appindicator). Where no `org.kde.StatusNotifierWatcher` exists (GNOME without the extension), closing the window quits the shell (the server keeps running) and the window's menu carries the tray's items. Plus the autostart entry for the tray, `xdg-open` for logs and links, and setup's Linux examples |
| 39.9 | **Updates on Linux** | on 38.12's next start: the Linux updater artifacts (`createUpdaterArtifacts`, the `linux-*` keys of `latest.json` for AppImage/deb/rpm, assembled once after every leg). After an update, the new shell runs `runtime install --activate`. Headless (the update path itself is 39.3's): the Epic 35 rollout runs `maktaba-server update` once 35.4's orchestrator installs by itself, and a new runtime that starts but never confirms (Epic 35's 3-minute confirmation) is switched back to `previous` |
| 39.10 | **Release CI** | `desktop-release.yml` Linux legs on ubuntu-22.04 (x86_64) and ubuntu-22.04-arm (aarch64): payload, then `tauri build --bundles appimage,deb,rpm` with the payload overlay, then `check.sh --app`; only checked bundles are attached, and one last job writes `latest.json` from everything attached. The packages' removal script stops and disables the app's user unit of every user whose user manager runs (never on an upgrade). `release.yml` publishes `maktaba-linux-payload-<v>-<arch>.tar.gz` (the name `maktaba-server-<v>-linux-<arch>.tar.gz` is the plain server archive's) and `install.sh` in `checksums.txt`, so a headless install verifies both before anything runs; an architecture whose payload failed is left out of the release and named in its notes instead of blocking it. `install.sh --app` installs a headless machine ([headless.md](headless.md)) |
| 39.11 | **End-to-end on a real systemd** | CI on an ubuntu runner with a user manager (linger on, `XDG_RUNTIME_DIR`): unpack the payload, `setup --answers`, and the unit runs every role. The web app and API answer on 127.0.0.1. A restart and a `systemctl --user kill` come back, and an update from a previous payload takes the backup, migrates, and keeps the venv when the constraints are unchanged |
| 39.12 | **Clean up** | docs (`docs/wiki/deployment.md`, `apps/desktop/README.md`, `deploy/packaging/README.md`): "Install on Linux" → the app or the headless payload. The nfpm system-unit .deb stays for operators who want a system service, documented as such |

## Order and parallel tracks

- **Track A (server, Go):** 39.5 → 39.3 (the runtime is built on the service, which it stops and
  starts); 39.4 on its own; then 39.6 (after 38.6) and 39.9 (after 38.12).
- **Track B (pipeline):** 39.7 (after 38.8).
- **Track C (shell, Rust + web):** 39.8 (needs 39.3, 39.5, and 38.3's split of `agent.rs` per OS).
- **Track D (packaging, CI):** 39.2 → 39.10 → 39.11 (39.2 starts on the layout above).
- **Then:** 39.12.

Each story ships as its own PR with tests, through the usual flow (CI green, review, merge).

## Shared with Epic 36 and Epic 38

| Piece | Built in | Linux adds |
|---|---|---|
| `setup --json` steps, the screens' platform wording | 38.6 | `check`'s Linux items, the terminal wizard (39.6) |
| supervisor's next start after an update | 38.12 | runtime activation before it (39.9) |
| launcher payload detection beyond `.app`, per-OS state root | 38.2 | the unresolved `runtime/current` and the owner's update environment (39.3), `--log-file` (39.5) |
| `[postgres] enabled = "auto"` in any payload | 38.10 | nothing |
| Whisper off Apple Silicon (CTranslate2), the second listen on torch | 38.8, 38.9 | Linux GPU detection and libraries (39.7) |
| `pgbin` (Postgres 16.15 per platform) | Epic 37 | the payload's pins are its pins (39.2); the test that keeps them equal comes once `pgbin` is on main |

## Owner decisions still open

- **The Tauri updater key pair** (as for the Mac and Windows): `plugins.updater.pubkey` is still
  `REPLACE_WITH_PUBKEY`. Without it no Linux build can update itself, and every update is a new
  download.
- **GNOME without the AppIndicator extension** shows no tray icon (Ubuntu ships the extension; Fedora
  Workstation does not). The app still works there (the window's menu has the same items). Should
  setup offer to open the extension's page?
- **CPU-only torch.** torch from PyPI brings its CUDA 13 libraries (~3 GB) to every Linux x86_64
  venv, NVIDIA or not. A CPU-only constraints variant (torch from the PyTorch CPU index) would save
  that on machines without NVIDIA, at the cost of a second exported lock. This is a follow-up, measured
  after 39.7.
