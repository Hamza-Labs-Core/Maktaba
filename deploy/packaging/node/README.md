# Maktaba node: worker-only install (Epic 31)

A **node** is a machine that joined a Maktaba home and works for it: it runs pipeline
workers on the home's database and queue, and reads and writes the media on the shared
storage (design: [`specs/epics/31-multi-instance/design.md`](../../../specs/epics/31-multi-instance/design.md)
section 7, and [`design-agent.md`](../../../specs/epics/31-multi-instance/design-agent.md)
for the cluster channel each worker opens). A node runs no API, no database and no web UI.

The files here install the node's worker as a service that starts at boot or logon,
restarts when it fails, and keeps its logs:

| OS | Service | Installer |
|---|---|---|
| macOS | LaunchAgent `com.maktaba.node` (+ `com.maktaba.node.<N>`) | `install-node.sh` |
| Linux | systemd user unit `maktaba-node.service` (+ `maktaba-node@<N>.service`); `--system` for system units | `install-node.sh` |
| Windows | Scheduled task `maktaba-node` (+ `maktaba-node@<N>`) at logon; `-Mode Service` for an NSSM service | `windows\install-node.ps1` |

Each installer:

1. builds the pipeline's Python environment (a snapshot of `pipeline/`, with uv when it is
   installed, else Python 3.12+ and pip);
2. writes `node.env` (optional settings) the first time, recording `MAKTABA_FFMPEG` /
   `MAKTABA_FFPROBE` when ffmpeg is installed somewhere the service would not look;
3. runs `maktaba-pipeline join` when the machine has not joined a home yet: it finds the
   home on the LAN (or takes `--home`), shows a 6-digit code, and waits while the admin
   types that code on the home (Admin → Machines, with "Accept new machines" on);
4. registers the main worker and any extra workers, and starts them.

It also puts a `maktaba-pipeline` command beside the environment (`join`, `cluster logs`,
...), linked into `~/.local/bin` (Linux system units: `/usr/local/bin`) when that exists.

## Before you start

- **The home** accepts machines: on the home, Admin → Machines → "Accept new machines"
  (it turns itself off after an hour).
- **The share** is mounted on this machine the way you use it every day (Finder, `/etc/fstab`,
  a mapped drive or `\\nas\share`). The worker works out where the library is mounted by
  itself; you can correct the path map on the home.
- **A checkout of this repository** (the installers build the worker from `pipeline/`).
- **uv** (recommended: <https://docs.astral.sh/uv/>), or Python 3.12+. uv installs the
  right Python by itself and installs exactly the locked versions.
- **ffmpeg** (`brew install ffmpeg`, `apt install ffmpeg`, `winget install Gyan.FFmpeg`).

## macOS

```sh
deploy/packaging/node/install-node.sh                       # finds the home on the LAN
deploy/packaging/node/install-node.sh --home http://192.168.1.20:8080 --name studio-2
deploy/packaging/node/install-node.sh --instances 2         # + 2 transcription workers
```

Run it as yourself, from a logged-in desktop session (not `sudo`). The worker is a
**LaunchAgent**: it runs as you, in your login session, because that is where the share
is mounted (Finder / `~/nas-nfs`, with your keychain's credentials) and where the node
config lives. It starts at login and stays up while you are logged in; on a dedicated
worker Mac, turn on automatic login. (A LaunchDaemon would start at boot, but would not
see shares mounted in your session nor your node config.)

On Apple Silicon the `stt` extra (MLX Whisper) is installed by default.

| What | Where |
|---|---|
| Node config (identity + home credentials) | `~/Library/Application Support/Maktaba/node.json` |
| Settings | `~/Library/Application Support/Maktaba/node.env`, `node@<N>.env` |
| Python environment, runner | `~/Library/Application Support/Maktaba/node/` |
| Agents | `~/Library/LaunchAgents/com.maktaba.node.plist`, `com.maktaba.node.<N>.plist` |
| Logs | `~/Library/Logs/Maktaba/node.log` (+ `node.err.log`), `node@<N>.log` |

```sh
launchctl print gui/$(id -u)/com.maktaba.node | grep -E 'state|last exit'   # status
launchctl kickstart -k gui/$(id -u)/com.maktaba.node                        # restart
launchctl bootout gui/$(id -u)/com.maktaba.node                             # stop (until next login)
tail -f ~/Library/Logs/Maktaba/node.log ~/Library/Logs/Maktaba/node.err.log  # logs
```

The plist runs `maktaba-node-run`, which reads `node.env` (then `node@<N>.env`) at every
start, rotates the instance's log files past 50 MB (`MAKTABA_NODE_LOG_MAX_MB`; three old
files kept), checks that the machine has joined, and `exec`s the worker. launchd itself
never rotates files, so rotation happens when a worker (re)starts.

## Linux

```sh
deploy/packaging/node/install-node.sh --home http://192.168.1.20:8080      # user units
sudo deploy/packaging/node/install-node.sh --system --run-as alice         # system units
```

**User units (default).** Run it as the user the worker should run as, logged in directly
(desktop or ssh, not `su`). The units go to `~/.config/systemd/user/`, and the installer
turns on lingering (`loginctl enable-linger`; `--no-linger` to skip) so the worker starts
at boot without a login. Use this when the share is mounted for that user (or system-wide)
and you want everything owned by a normal account.

**System units (`--system`).** For a headless box whose share is mounted system-wide
(`/etc/fstab` with `_netdev`, autofs). The units go to `/etc/systemd/system/`, are ordered
after `network-online.target` and `remote-fs.target`, carry systemd hardening, and run as
`--run-as USER` (default `maktaba`, created when missing). That account must be able to
read and write the share; the installer joins as that account, so the node config is
`~USER/.config/maktaba/node.json`.

A user manager cannot wait for the system's `network-online.target` / `remote-fs.target`,
so a user unit started before the network or the share is up fails its first database
connect and is restarted 30 s later until it gets through.

| What | User units | System units |
|---|---|---|
| Node config | `~/.config/maktaba/node.json` | `~USER/.config/maktaba/node.json` |
| Settings | `~/.config/maktaba/node.env`, `node@<N>.env` | `/etc/maktaba/node.env`, `node@<N>.env` |
| Python environment | `~/.local/share/maktaba/node/` | `/opt/maktaba/node/` |
| Units | `~/.config/systemd/user/maktaba-node{,@}.service` | `/etc/systemd/system/maktaba-node{,@}.service` + `*.d/10-run-as.conf` |
| Logs | journald | journald |

```sh
systemctl --user status maktaba-node 'maktaba-node@*'       # status (system units: drop --user, add sudo)
systemctl --user restart maktaba-node                       # restart
systemctl --user stop maktaba-node                          # stop (until next boot)
journalctl --user -u maktaba-node -f                        # logs
journalctl --user -u 'maktaba-node@*' --since today         # the extra workers
```

On distributions with a volatile journal, user-unit logs land in the system journal:
`journalctl _SYSTEMD_USER_UNIT=maktaba-node.service -f`. Logs are JSON lines
(`MAKTABA_ENV=prod`); put `MAKTABA_ENV=dev` in `node.env` for readable ones.

## Windows

From a normal (not elevated) PowerShell, as the user whose drives and share credentials
the worker should use:

```powershell
powershell -ExecutionPolicy Bypass -File deploy\packaging\node\windows\install-node.ps1 -HomeUrl http://192.168.1.20:8080
powershell -ExecutionPolicy Bypass -File deploy\packaging\node\windows\install-node.ps1 -Ffmpeg C:\tools\ffmpeg\bin\ffmpeg.exe
```

**Scheduled task (default).** One task per worker, "At log on" of this user (30 s delay so
mapped drives reconnect), running as this user, hidden, with no time limit, normal
priority, and restart when the task fails. It runs `run-node.ps1`, which restarts the
worker itself after a crash (5 s, doubling to 30 s) and stops for good after a clean exit.
Mapped drive letters, `net use` and Credential Manager entries are per user, and so is the
node config in `%APPDATA%`: the per-user task runs where they are. It needs no
administrator rights. It runs only while the user is logged on: on a dedicated worker PC,
turn on automatic logon.

**Service (`-Mode Service`).** One Windows service per worker through
[NSSM](https://nssm.cc) (`nssm.exe` on the PATH, elevated PowerShell). It starts at boot
with no logon (delayed auto-start) and runs as your account; the installer asks for your
password and hands it to NSSM (it appears briefly on NSSM's command line). Services run
in session 0 and **do not see mapped drive letters**: the share must be reachable as a UNC
path (`\\nas\share`), and the path map on the home must use it. `sc.exe` alone is not an
option: the worker is a Python program, not a service program, and the service manager
would kill it after 30 s; NSSM is the wrapper that answers the service manager.

| What | Where |
|---|---|
| Node config | `%APPDATA%\Maktaba\node.json` |
| Settings | `%APPDATA%\Maktaba\node.env`, `node@<N>.env` |
| Python environment, scripts | `%LOCALAPPDATA%\Maktaba\node\` |
| Logs | `%LOCALAPPDATA%\Maktaba\Logs\node.log` (+ `node.err.log`), `node@<N>.log` |

```powershell
Get-ScheduledTask -TaskName 'maktaba-node*' | Get-ScheduledTaskInfo         # status
Stop-ScheduledTask -TaskName maktaba-node; Start-ScheduledTask -TaskName maktaba-node   # restart
Get-Content -Wait "$env:LOCALAPPDATA\Maktaba\Logs\node.log"                  # logs
Get-Content -Tail 50 "$env:LOCALAPPDATA\Maktaba\Logs\node.err.log"           # errors + run-node's own lines
& "$env:LOCALAPPDATA\Maktaba\node\maktaba-pipeline.cmd" join                 # the pipeline command
```

Stopping a task kills `run-node.ps1`; the worker goes with it (a job object ties them).
The worker runs with `PYTHONUTF8=1`, so log files and Arabic file names stay UTF-8.

## More than one worker per machine

`--instances N` (`-Instances N` on Windows) adds N extra **transcription** workers beside
the main one, like the Mac's `tools/dev/stack.sh` does: worker `<i>` claims only the
`transcribe` stage and has `MAKTABA_STT_SLOT=stt-<i>` (its speech-to-text metrics series,
kept across restarts), and the main worker then leaves `transcribe` to them
(`MAKTABA_PIPELINE_EXCLUDE_STAGES=transcribe`). Names: `com.maktaba.node.<i>`,
`maktaba-node@<i>.service`, task `maktaba-node@<i>`.

Running the installer again without `--instances` keeps the workers you have; with it,
adds or removes workers to match. Transcription runs on Apple Silicon only today; on
other machines give an extra worker other stages in its `node@<i>.env`
(`MAKTABA_PIPELINE_STAGES=...`). The home's per-machine stage switches apply on top.

## Settings

`node.env` (and `node@<N>.env` for one worker, which wins) holds `KEY=VALUE` lines, read
at every start: copy of [`node.env.example`](node.env.example), every line commented out
except what the installer found. The variables: `MAKTABA_FFMPEG` / `MAKTABA_FFPROBE`,
`MAKTABA_STORAGE_HINTS`, `MAKTABA_WORKER_PATH_MAP`, `MAKTABA_PIPELINE_STAGES` /
`..._EXCLUDE_STAGES`, `MAKTABA_NODE_CONFIG`, the cluster channel's `MAKTABA_SHARE_ROOT`,
`MAKTABA_CLUSTER_DIR`, `MAKTABA_CLUSTER_RETENTION_DAYS`, `MAKTABA_CLUSTER_FILE_MB`, the
LLM runtimes' `MAKTABA_OLLAMA_URL`, `MAKTABA_LLM_ENDPOINTS`, `MAKTABA_LLM_MAX_PARALLEL`,
and `MAKTABA_ENV`, `MAKTABA_NODE_LOG_MAX_MB`. Restart the workers after editing.

`MAKTABA_DATABASE_URL` is always removed from a node worker's environment: a node takes
its database from its node config, and that variable would make it a home worker.

## Updating

```sh
git pull
deploy/packaging/node/install-node.sh            # same scope flags as before (--system)
```

```powershell
git pull
powershell -ExecutionPolicy Bypass -File deploy\packaging\node\windows\install-node.ps1
```

The installer remembers the extras, the source directory, the account (`--run-as`) and the
Windows mode; it rebuilds the environment (the pipeline package is always reinstalled,
the dependencies follow `uv.lock`), re-renders the units, and restarts the workers. The
node config is untouched: the machine stays joined.

## Removing

```sh
deploy/packaging/node/uninstall-node.sh              # or: install-node.sh --uninstall
sudo deploy/packaging/node/uninstall-node.sh --system
deploy/packaging/node/uninstall-node.sh --purge      # also logs and node*.env
```

```powershell
powershell -ExecutionPolicy Bypass -File deploy\packaging\node\windows\uninstall-node.ps1 [-Purge]
```

This stops and removes the services and the Python environment. The **node config is
kept**: it is the machine's identity and holds the home's database credentials. To be done
with the home, remove the machine there (Admin → Machines → Revoke, then Remove), then
delete `node.json`. Linux: lingering and the `maktaba` account are left as they are.

## When the home is unreachable, revoked, or not joined

| Situation | What the service does |
|---|---|
| The home (its database) is down or not reachable at start: boot before the network, home rebooting | The worker fails its first connect and exits non-zero; the service manager starts it again after 30 s (systemd `Restart=on-failure`, `RestartSec=30`, no start limit; launchd `KeepAlive` on failure, `ThrottleInterval` 30; Windows `run-node.ps1` 5 s doubling to 30 s), for as long as it takes. |
| The home goes away while the worker runs | The worker keeps running: its heartbeat failures are logged, the database pool reconnects when the home is back. Jobs it had claimed may be handed to other workers by the home's reaper; transcriptions resume from their checkpoints. The cluster channel buffers logs and retries every few seconds up to 30 s. |
| The share is not mounted yet | The worker starts and stops claiming the stages that need the share (its storage check, every 5 min, says why on the home's Machines page); they come back when the share is. |
| The admin revokes the machine | The worker sees it at its next heartbeat (within 10 s) and exits 0: a clean exit, so it is **not** restarted. At the next boot or login it starts again and its database login is refused, so it keeps failing every 30 s: uninstall a revoked machine (or join again). |
| The machine has not joined (or `node.json` was deleted) | The service does not start the worker (`node_check.py`, exit 78) and checks again every 30 s, so it starts by itself once `maktaba-pipeline join` completes. Without this check `maktaba-pipeline run` would fall back to an in-memory database. |
| Drain on the home | The worker finishes what it has and claims nothing new; it stays up. |

## Files

| Path | What |
|---|---|
| `install-node.sh`, `uninstall-node.sh` | macOS + Linux installer (`--help`; `--dry-run` prints the commands and renders the files under a temporary directory) |
| `node_check.py` | "has this machine joined?" (exit 0 / 78), run before every start and by the installers |
| `node.env.example` | the settings file, all commented |
| `launchd/com.maktaba.node.plist` | LaunchAgent template (`__PLACEHOLDERS__` filled by the installer) |
| `launchd/maktaba-node-run` | the agent's runner: settings, log rotation, join check, `exec` |
| `systemd/user/maktaba-node{,@}.service` | user units (main, template for extra workers) |
| `systemd/system/maktaba-node{,@}.service` | system units (`User=` set by a drop-in) |
| `windows/install-node.ps1`, `uninstall-node.ps1` | Windows installer (Windows PowerShell 5.1+) |
| `windows/run-node.ps1`, `node-common.ps1` | the task's / service's supervisor, shared helpers |

## Checks

```sh
plutil -lint deploy/packaging/node/launchd/com.maktaba.node.plist
shellcheck deploy/packaging/node/*.sh deploy/packaging/node/launchd/maktaba-node-run
systemd-analyze verify deploy/packaging/node/systemd/system/*.service      # on Linux
systemd-analyze --user verify deploy/packaging/node/systemd/user/*.service
deploy/packaging/node/install-node.sh --dry-run --instances 2              # renders + lints the plists
MAKTABA_NODE_OS=linux deploy/packaging/node/install-node.sh --dry-run --system   # Linux files, from any host
```

```powershell
foreach ($f in Get-ChildItem deploy\packaging\node\windows\*.ps1) {
    $errors = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$null, [ref]$errors)
    if ($errors) { $errors } else { "$($f.Name): OK" }
}
```
