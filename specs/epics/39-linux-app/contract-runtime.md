# Epic 39 — contract: the runtime copy and the user service (stories 39.3, 39.5)

> **Status:** contract for 39.3 and 39.5 (Linux; the Windows app stops its server before the
> installer runs instead, Epic 38.12). **Callers:** the desktop shell (39.8), the setup steps (39.6),
> the headless updater (39.9). **Implements it:** `cmd/maktaba-server` (`runtime`, `service`,
> `update`), `cmd/maktaba-launcher` and the API's self-update.

## 1. Why a copy

The payload ships inside something that is not a good place to run a server from:

| Ships in | Problem |
|---|---|
| the AppImage | mounted at a new `/tmp/.mount_*` on every run, only while it runs, and through FUSE: the venv's interpreter path would move every start |
| the .deb / .rpm (`/usr/share/maktaba/server`) | the package manager replaces files under a running server: a worker started after that runs the new Python against the old venv |
So the server always runs from a copy the user owns, and a new payload starts only when Maktaba
switches to it, with the server stopped.

## 2. Layout

The root is the platform's **default** data directory, never server.toml's `data_dir`. The unit
names the root, so moving the data must not break it. The data directory and the root are created
`0700` (the user's only); the lock is `0600`.

```
~/.local/share/maktaba/runtime/                 ($XDG_DATA_HOME/maktaba/runtime)
  v0.3.0_1a2b3c4d/      a payload: bin/ postgres/ python/ pipeline/ licenses/
                        payload.json  files.sha256   (the bytes the install verified)
                        .installed   {"version","platform","files_sha256","from","installed_at"}
  v0.2.1_9f8e7d6c/      the one that ran before (kept: a rollback goes back to it, and its
                        Postgres can pg_upgrade a major bump)
  current -> v0.3.0_1a2b3c4d     the runtime the service runs (a relative symlink)
  previous -> v0.2.1_9f8e7d6c    the runtime that ran before it (a relative symlink)
  watermark             the newest version this runtime ever ran, one line; it never falls
  owner.json            {"owner": "app" | "headless"}
  lock                  one install or activation at a time
  .staging-<dir>-<rand>/  while copying; never taken for an install, removed by the next one
  .download-<rand>/     a headless update's tarball and its unpacked payload (removed after it,
                        or by an install or prune once it is 6 hours old)
```

- **The payload's manifest** is `files.sha256` (39.2's `payload.sh`): one line per file
  (`f <mode> <size> <sha256> <path>`) and per symlink (`l <target> <path>`), tab-separated, sorted
  by path. payload.json's `files` names it: `{"manifest": "files.sha256", "count", "bytes",
  "sha256"}`, where `sha256` is the manifest's own SHA-256.
- **The directory name** is `<payload.json version>_<first 8 hex of files.sha256's SHA-256>`, so two
  builds with the same `git describe` never share a directory.
- **`owner`**: `app` when the desktop shell manages updates; `headless` when the machine updates
  itself through Epic 35's verified releases (§4). No owner.json is `app`.

## 3. `maktaba-server runtime …`

Every command takes `--json`: NDJSON events as in [setup's contract](../36-mac-app/contract-setup.md#events)
(`progress` lines, then one `result` or `error`). Exit codes: 0 result, 1 error, 2 usage.

| Command | Does |
|---|---|
| `runtime install [--from DIR] [--owner app\|headless] [--activate] [--allow-downgrade]` | `--from` defaults to the payload this binary runs from. Removes what an install cut short left, checks the room, then copies every file into `.staging-*`, checking it against `files.sha256` as it goes (`stage: copy`, bytes `done`/`total`). Writes the payload.json and files.sha256 it verified (not the source read again), then the directory is flushed and renamed to its final name. If that name exists already with the same manifest, nothing is copied. **The first runtime** (no `current` yet) becomes `current` at once, so an install alone leaves something the service can run (`"activated": true`). With `--activate`: stop the service (if installed), point `current` at it, start the service, prune |
| `runtime activate <dir\|previous> [--allow-downgrade]` | the same switch, for an installed directory; `previous` is the one that ran before (a rollback) |
| `runtime status [--from DIR]` | `current`, its `version`, `previous`, `watermark`, `owner`, the installed directories; and for the payload this binary runs from (or `--from`): `from`, `from_version`, `update_pending` (it is newer than current, or the same version built again; the app activates only a newer one: the same release built again is kept as it is, and switching is the server's, D3) and `downgrade` (it is older than the watermark) |
| `runtime prune [--keep N]` | keeps `current`, `previous` and the newest other installs up to N in all (N ≥ 2), removes the rest. Never `current` or `previous` |

**Downgrades.** A version older than the watermark (compared as release versions, a leading `v`
ignored) is refused by `install` and `activate` with `downgrade`, unless `--allow-downgrade`. The
database of the newer version may be migrated past what the older one knows. The watermark rises
when a runtime becomes `current` and never falls, so an allowed downgrade does not lower it.
Installing a directory that is installed already copies nothing and is never refused, and
activating the one that is `current` changes nothing: after an allowed downgrade, the older app
keeps starting. This is Epic 35's rule ("never older than the newest version installed here"). The
desktop shell sees `downgrade: true` in `status` when an older app starts and keeps the running
server, telling the user the app is older than the server (39.8).

Errors: `payload_invalid` (no or bad payload.json, a file missing or changed: the message names it),
`disk_full` (checked before copying: the payload's size plus 10%), `locked` (another install runs:
waited for 2 minutes, then this), `not_installed` (`activate` of a directory that is not an installed
runtime, or `previous` with none recorded), `downgrade` (above), `service` (stopping or starting
failed: `current` is left as it was before, or put back).

**Activation order** (the server never meets a half-switched runtime): stop the service and wait for
it to be inactive → point `current` at the new directory (create `current.new` and rename it over
`current`: atomic on Linux) → start. If the start fails, `current` goes back and the old one is
started again. Once it is switched, `previous` names the old directory and the watermark rises to
the new version. Then the supervisor's next start runs [36.8's contract](../36-mac-app/contract-update.md#what-the-supervisor-does-on-its-next-start-track-a)
(backup, migrate, venv) against `runtime/current/payload.json`.

## 4. The launcher in a runtime

- **App-bundle mode** is found by `payload.json` beside `bin/` (Epic 38.2's detection, which also
  serves the Windows install's `server\bin`), as well as by the `.app` path. A runtime directory is
  such a payload, so the same launcher runs Maktaba.app, the Windows app and a Linux runtime. Linux
  also checks that payload.json's `platform` matches the machine.
- **`MAKTABA_APP_BUNDLE`** is `<root>/current` (unresolved) when `current` resolves to the
  launcher's own payload, else the payload directory itself. The venv is built against
  `runtime/current/python/bin/python3`, so a new payload with the same Python minor version keeps
  working with no venv rebuild.
- **The state root** (its lock and `launcher.log` in `updates/`) is the platform's default data
  directory: `$XDG_DATA_HOME/maktaba` (`~/.local/share/maktaba`) on Linux, wherever the runtime is;
  `--root` or `$MAKTABA_INSTALL_ROOT` override it (macOS and Windows as 38.2 has them).
- **`--log-file PATH`** sends the launcher's and the server's output to PATH. It is renamed to
  `PATH.1` at start when larger than 50 MB, and again as soon as a write would take it past that
  (one old copy kept). The logs directory is `0700`, the log `0600`. Built in 39.5, since the unit
  uses it.
- **Updates** follow the runtime's owner, read from `owner.json` at every server start (a change
  applies at the next one). The launcher sets `MAKTABA_RUNTIME_OWNER` for the server and every role.
  - `owner: app`: also `MAKTABA_UPDATE_DISABLE=1` (one updater, the app's). `maktaba-server update`
    refuses and says the app updates it.
  - `owner: headless`: no `MAKTABA_UPDATE_DISABLE`, so the API's update check runs. `maktaba-server
    update` verifies the release (Epic 35: the Sigstore bundle over `checksums.txt` from the release
    workflow of that tag, Rekor, `release.json`), downloads `maktaba-linux-payload-<v>-<arch>.tar.gz`
    into `.download-*`, checks it against `checksums.txt`, unpacks it (files, directories and
    symlinks only; no path out of the directory; links made last), checks its payload.json is that
    release's version, then `runtime install` and `runtime activate`, then prune. `--rollback`
    activates `previous`. The running binary is never replaced.
  - The API's `POST /api/admin/system/update` in a headless runtime starts that command as a
    transient user unit (`systemd-run --user --collect --unit maktaba-update-<time> -- <runtime
    dir>/bin/maktaba-server update --version <v>`, with `MAKTABA_UPDATE_REPO`, `MAKTABA_GITHUB_TOKEN`
    and the `XDG_*` directories passed by name, never on the command line) and answers 202 with the
    unit's name. A child of the API would die with the service when activation stops it.

## 5. `maktaba-server service …` (Linux: a systemd user unit)

| Command | Does |
|---|---|
| `service install [--start] [--replace]` | writes the unit (below) where the user's systemd reads units: `~/.config/systemd/user`, or `$XDG_CONFIG_HOME/systemd/user` when the user manager has that variable (`systemctl --user show-environment`), never a shell's own. Then `daemon-reload`, `enable`, and with `--start` (the default) `start` |
| `service uninstall` | `disable --now`, removes the unit, `daemon-reload` (data untouched) |
| `service start \| stop \| restart` | `systemctl --user …`; `stop` waits until inactive |
| `service linger [--off]` | `loginctl enable-linger` (the server starts at boot, without a login) |
| `service status` | `{"unit","installed","taken","system_unit","enabled","active","sub_state","main_pid","linger","manager","reason"}`. `manager`: `ok`, or `unsupported` (no systemd user manager: not PID 1, no `XDG_RUNTIME_DIR`) with the reason |

```ini
# Maktaba (Epic 39): written by `maktaba-server service install`; it is replaced on every install.
[Unit]
Description=Maktaba (the server and everything it runs)

[Service]
Type=simple
Environment="XDG_CONFIG_HOME=/home/<u>/.config"
Environment="XDG_DATA_HOME=/home/<u>/.local/share"
Environment="XDG_STATE_HOME=/home/<u>/.local/state"
Environment="XDG_CACHE_HOME=/home/<u>/.cache"
ExecStart=/home/<u>/.local/share/maktaba/runtime/current/bin/maktaba-launcher --log-file /home/<u>/.local/state/maktaba/logs/server.log serve
Restart=always
RestartSec=5
RestartForceExitStatus=75
KillMode=mixed
TimeoutStopSec=90
LimitNOFILE=65536

[Install]
WantedBy=default.target
```

- Paths are absolute, resolved from `$XDG_DATA_HOME` / `$XDG_STATE_HOME` when the unit is written,
  and quoted with systemd's rules when they hold spaces (`%` and `$` doubled).
- **The `XDG_*` directories are pinned** in `Environment=` with the values (or defaults) of the
  shell that ran `service install`. A user unit gets the manager's environment, not the shell's, so
  without them a custom `XDG_DATA_HOME` would put the server's data somewhere setup never wrote.
- `KillMode=mixed`: SIGTERM reaches the launcher only. It asks the supervisor to drain, which stops
  Postgres last. `TimeoutStopSec=90` covers the launcher's 60 s stop grace before systemd kills. On
  Linux the launcher is the roles' subreaper: a role orphaned by a crashed server is stopped before
  the server is started again.
- **Pause / Resume** are `stop` / `start`. A stopped unit stays stopped until Resume or the next boot.
- **Another user unit of that name.** The unit's name is the one Epic 35's user units use. If a
  self-managed install (a layout root with `current`) owns it, `service install` refuses with
  `service_taken`, naming what it runs; `--replace` takes it over.
- **A system-wide `maktaba-server.service`** (in `/etc/systemd/system`, `/run/systemd/system`,
  `/usr/local/lib/systemd/system`, `/usr/lib/systemd/system` or `/lib/systemd/system`: the nfpm
  package's, or `install.sh` run as root) is another server on the same ports. `service install`
  refuses with `service_taken` even with `--replace`, names the unit and says how its owner removes
  it (`sudo apt remove maktaba-server` / `sudo systemctl disable --now maktaba-server`). `status`
  reports it as `system_unit`, and setup's `check` (39.6) stops on it first.
- **Linger:** polkit allows `set-self-linger` from an active local session without a password. Over
  SSH, `loginctl` asks for the admin password in the terminal (the headless setup runs it there).
  Without linger the server still runs, but only while the user is logged in. Setup and `status`
  say so.

## Tests

- `runtime`: install from a fake payload (copy, a changed file refused, a second install a no-op,
  the first install made `current`, leftovers removed before the room is checked, the verified bytes
  written), activate with a fake service (stop → flip → start, a failed start flipped back, a failed
  stop leaves `current`), downgrades refused and allowed, `previous` and prune keeping it over a newer
  install that never ran, the lock, the error codes; the payload tarball unpacked (a path out, a
  device, a file through a link refused); `update` of a headless runtime against a fake release
  (check only, install and activate, up to date, rollback, a refused release, a tarball of another
  version), and refused for an app-owned one.
- The launcher: payload detection (`.app` path, `payload.json`, a wrong platform refused), the
  unresolved `current`, the state root, the update environment per owner, log rotation and modes,
  a crashed server restarted and its orphaned roles stopped.
- The API: Update now in a headless runtime starts the transient unit and answers 202; the
  `systemd-run` arguments carry no secret.
- `service`: the unit text (golden), the plan of commands, the manager's unit directory, the pinned
  `XDG_*`, a system-wide unit refused, status parsing of `systemctl --user show` output. On a real
  user manager: 39.11's end-to-end job.
