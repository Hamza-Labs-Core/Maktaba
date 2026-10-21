# Epic 39 — contract: the runtime copy and the user service (stories 39.3, 39.5)

> **Status:** contract for 39.3 and 39.5 (Linux; the Windows app stops its server before the
> installer runs instead, Epic 38.12). **Callers:** the desktop shell (39.8), the setup steps (39.6),
> the headless updater (39.9). **Implements it:** `cmd/maktaba-server` (`runtime`, `service`) and
> `cmd/maktaba-launcher`.

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
names the root, so moving the data must not break it.

```
~/.local/share/maktaba/runtime/                 ($XDG_DATA_HOME/maktaba/runtime)
  v0.3.0_1a2b3c4d/      a payload: bin/ postgres/ ffmpeg/ python/ pipeline/ licenses/ payload.json
                        + .installed   {"version","files_sha256","from","installed_at"}
  v0.2.1_9f8e7d6c/      the one before (kept: its Postgres can pg_upgrade a major bump)
  current -> v0.3.0_1a2b3c4d     a relative symlink
  owner.json            {"owner": "app" | "headless", "app": "<the AppImage or /usr/bin/Maktaba>"}
  lock                  one install at a time
  .staging-<v>-<rand>/  while copying; never taken for an install
```

- **The directory name** is `<payload.json version>_<first 8 hex of files_sha256>`, so two builds
  with the same `git describe` never share a directory.
- **`files_sha256`** is the SHA-256 of payload.json's `files` list (path + SHA-256 of every file,
  sorted), written by `payload.sh` (39.2).
- **`owner`**: `app` when the desktop shell manages updates (the launcher sets
  `MAKTABA_UPDATE_DISABLE=1`, as in Maktaba.app); `headless` when the machine updates itself through
  Epic 35 (39.9).

## 3. `maktaba-server runtime …`

Every command takes `--json`: NDJSON events as in [setup's contract](../36-mac-app/contract-setup.md#events)
(`progress` lines, then one `result` or `error`). Exit codes: 0 result, 1 error, 2 usage.

| Command | Does |
|---|---|
| `runtime install [--from DIR] [--owner app\|headless] [--activate]` | `--from` defaults to the payload this binary runs from. Checks every file against `files` while copying into `.staging-*` (`stage: copy`, bytes `done`/`total`). The directory is then fsynced and renamed to its final name. If that name exists already with the same `files_sha256`, nothing is copied. With `--activate`: stop the service (if installed), point `current` at it, start the service, prune |
| `runtime activate <dir>` | the same switch, for an installed directory |
| `runtime status` | `current`, `previous`, the installed directories, `owner`, and when run from a payload, whether that payload differs from `current` (`update_pending`) |
| `runtime prune [--keep N]` | removes directories beyond `current` and the newest other one (N=2). Never `current` |

Errors: `payload_invalid` (no or bad payload.json, a file missing or changed: the message names it),
`disk_full` (checked before copying: the payload's size plus 10%), `locked` (another install runs:
waited for 2 minutes, then this), `service` (stopping or starting failed: `current` is left as it was
before, or put back).

**Activation order** (the server never meets a half-switched runtime): stop the service and wait for
it to be inactive → point `current` at the new directory (create `current.new` and rename it over
`current`: atomic on Linux) → start. If the start fails, `current` goes back and the old one is
started again. Then the supervisor's next start runs [36.8's contract](../36-mac-app/contract-update.md#what-the-supervisor-does-on-its-next-start-track-a)
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
  directory: `$XDG_DATA_HOME/maktaba` on Linux (macOS and Windows as 38.2 has them).
- **`--log-file PATH`** sends the launcher's and the server's output to PATH. It is renamed to
  `PATH.1` at start when larger than 50 MB, and again as soon as a write would take it past that
  (one old copy kept). Built in 39.5, since the unit uses it.
- **Updates:** with `owner: app`, the launcher sets `MAKTABA_UPDATE_DISABLE=1` (one updater, the
  app's). With `owner: headless`, it does not, and `maktaba-server update` installs a new runtime
  (39.9) instead of replacing its own executable.

## 5. `maktaba-server service …` (Linux: a systemd user unit)

| Command | Does |
|---|---|
| `service install` | writes `~/.config/systemd/user/maktaba-server.service` (below), `daemon-reload`, `enable --now` |
| `service uninstall` | `disable --now`, removes the unit, `daemon-reload` (data untouched) |
| `service start \| stop \| restart` | `systemctl --user …`; `stop` waits until inactive |
| `service linger [--off]` | `loginctl enable-linger` (the server starts at boot, without a login) |
| `service status` | `{"installed","enabled","active","linger","manager","reason"}`. `manager`: `ok`, or `unsupported` (no systemd user manager: not PID 1, no `XDG_RUNTIME_DIR`) with the reason |

```ini
[Unit]
Description=Maktaba (the server and everything it runs)

[Service]
Type=simple
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

- Paths are absolute (resolved from `$XDG_DATA_HOME` / `$XDG_STATE_HOME` when the unit is written),
  quoted with systemd's rules when they hold spaces.
- `KillMode=mixed`: SIGTERM reaches the launcher only. It asks the supervisor to drain, which stops
  Postgres last. `TimeoutStopSec=90` covers the launcher's 60 s stop grace before systemd kills.
- **Pause / Resume** are `stop` / `start`. A stopped unit stays stopped until Resume or the next boot.
- The unit's name is the one Epic 35's user units use. If a self-managed install (a layout root
  with `current`) already owns it, `service install` refuses with `service_taken`, naming that root.
  `check` reports the same thing first.
- **Linger:** polkit allows `set-self-linger` from an active local session without a password. Over
  SSH, `loginctl` asks for the admin password in the terminal (the headless setup runs it there).
  Without linger the server still runs, but only while the user is logged in. Setup and `status`
  say so.

## Tests

- `runtime`: install from a fake payload (copy, a changed file refused, a second install a no-op),
  activate with a fake service (stop → flip → start, a failed start flipped back), prune, the lock,
  a staging directory left by a kill ignored and removed.
- The launcher: payload detection (`.app` path, `payload.json`, a wrong platform refused), the
  unresolved `current`, log rotation.
- `service`: the unit text (golden file), the plan of commands, status parsing of
  `systemctl --user show` output. On a real user manager: 39.11's end-to-end job.
