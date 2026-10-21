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

1. builds the pipeline's Python environment (with uv when it is installed, else Python
   3.12+ and pip) from [the release's wheel](#where-the-pipeline-comes-from), or from a
   checkout's `pipeline/`, and checks that its `maktaba-pipeline run` has `--node`;
2. writes `node.env` (optional settings) the first time, recording `MAKTABA_FFMPEG` /
   `MAKTABA_FFPROBE` when ffmpeg is installed somewhere the service would not look;
3. runs `maktaba-pipeline join` when the machine has not joined a home yet
   (`node_check.py` decides): it finds the home on the LAN (or takes `--home`), shows a
   6-digit code, and waits while the admin types that code on the home (Admin → Machines,
   with "Accept new machines" on);
4. registers the main worker and any extra workers, and starts them.

Every worker runs **`maktaba-pipeline run --node`**, the venv's own `maktaba-pipeline`:
in node mode it works for the home in its node config, or exits **78** when the machine
has not joined (it never falls back to another database), and exits **77** when the home
has removed the machine for good. The services retry a 78 every 30 s and stop on a 77
(see [the failure table](#when-the-home-is-unreachable-removed-or-not-joined)).

The venv's `maktaba-pipeline` is also the command you use (`join`, `cluster logs`, ...):
macOS and Linux link it as `<node dir>/bin/maktaba-pipeline`, and into `~/.local/bin`
(Linux system units: `/usr/local/bin`) when that exists; Windows uses
`%LOCALAPPDATA%\Maktaba\node\venv\Scripts\maktaba-pipeline.exe`. For a pipeline without
that entry point, the installers write a stand-in that runs `python -m maktaba_pipeline`.

## Getting the installer

Every release attaches the node's assets next to the server archives:

| Asset | What |
|---|---|
| `maktaba-node-<version>.tar.gz` | this directory for macOS and Linux, with the wheel, its constraints and `release.conf` |
| `maktaba-node-<version>.zip` | the same, for Windows |
| `maktaba_pipeline-<version>-py3-none-any.whl` | the pipeline, stamped with the release's version |
| `maktaba-pipeline-<version>-constraints.txt` | the pipeline's dependencies as `uv.lock` pins them (every extra but `dev`) |

They are in the release's `checksums.txt`, signed with cosign like the rest by the release
workflow of exactly that tag (how to check
the archive: [join a machine](../../../docs/cluster/join-a-machine.md#the-easy-way-the-node-installer)).
On the machine:

```sh
v=1.4.0   # the home's version: a node and its home run the same release
curl -fsSLO "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v$v/maktaba-node-$v.tar.gz"
tar -xzf "maktaba-node-$v.tar.gz"
"./maktaba-node-$v/install-node.sh" --home http://192.168.1.20:8080
```

```powershell
$v = '1.4.0'
Invoke-WebRequest "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v$v/maktaba-node-$v.zip" -OutFile "maktaba-node-$v.zip" -UseBasicParsing
Expand-Archive "maktaba-node-$v.zip" -DestinationPath .
powershell -ExecutionPolicy Bypass -File ".\maktaba-node-$v\windows\install-node.ps1" -HomeUrl http://192.168.1.20:8080
```

A git checkout works too: `deploy/packaging/node/` of the checkout is the same directory,
and its installer builds the checkout's `pipeline/`. The examples below use the archive's
`./maktaba-node-<version>/` path; in a checkout it is `deploy/packaging/node/`.

### Where the pipeline comes from

The first of these that applies:

| | Installs |
|---|---|
| `--from-wheel URL\|PATH` (`-FromWheel`) | that wheel, a file or an http(s) URL (downloaded into `<node dir>/dist/`); `--constraints URL\|PATH` (`-Constraints`) pins its dependencies |
| `--source DIR` (`-Source`) | a build of that source directory, pinned by its `uv.lock` |
| the last install's choice | the same as last time (`<node dir>/install.conf`: `FROM=release`, `wheel` or `source`) |
| `release.conf` beside the installer | **the release's wheel**: the one in the archive (its SHA-256 checked), else downloaded from the release; pinned by the release's constraints file; uv gives the environment the release's Python (3.12) |
| neither | the checkout's `pipeline/` (`uv sync --frozen` of its `uv.lock`) |

`--from-wheel` and `--source` exclude each other. A wheel given without constraints still
installs, with a warning: its dependencies then resolve to the newest versions its
metadata allows, not the tested ones.

**Digests.** A wheel or constraints file whose SHA-256 is known is checked before it is
installed; one that does not match is refused (a refused download is deleted):

| File | Checked against |
|---|---|
| a release's wheel and constraints | `release.conf`'s `WHEEL_SHA256` / `CONSTRAINTS_SHA256` |
| `--from-wheel` / `--constraints` with `--sha256 HEX` / `--constraints-sha256 HEX` (`-Sha256`, `-ConstraintsSha256`) | that digest, remembered in `install.conf` |
| a URL given without a digest | nothing the first time; its SHA-256 is then pinned in `install.conf` (`WHEEL_SHA256`, `CONSTRAINTS_SHA256`), so an update that downloads a different file from the same URL is refused |
| a local file given without a digest | not pinned: a wheel rebuilt at the same path installs on the next update |

To take a new file from a pinned URL, pass `--from-wheel` (and `--constraints`) again: an
explicit flag starts a new pin. The digest comes from `sha256sum`, else `shasum`, else
Python's `hashlib` (Windows: `Get-FileHash`); with none of them the installer stops
rather than install unchecked.

**Dependencies, hash-checked.** A release's constraints file carries every pinned
dependency's hashes from `uv.lock` (`build-release.sh` refuses to publish one without), and
the installers install with `--require-hashes`: each package downloaded from PyPI must match
one of its hashes, and the wheel its own SHA-256. A constraints file without hashes is
refused. Without uv, the venv's own pip installs the constraints' pins (hash-checked, every
extra's) and then the wheel; it is not upgraded from PyPI first.

**URLs with credentials are refused.** A `--from-wheel` / `--constraints` URL is printed
while it downloads and remembered in `install.conf`, so one with user info
(`https://user:token@host/...`) or a query string (`?token=...`, a signed URL) is refused,
shown redacted. Use a plain URL pinned with `--sha256` / `--constraints-sha256`, or download
the file and pass its path.

Downloads go to `<node dir>/dist/`; after a successful install only the files that
install used are kept there.

## Before you start

- **The home** accepts machines: on the home, Admin → Machines → "Accept new machines"
  (it turns itself off after an hour).
- **The share** is mounted on this machine the way you use it every day (Finder, `/etc/fstab`,
  a mapped drive or `\\nas\share`). The worker works out where the library is mounted by
  itself; you can correct the path map on the home.
- **The release's node archive** ([above](#getting-the-installer)), or a checkout of this
  repository.
- **uv** (recommended: <https://docs.astral.sh/uv/>), or Python 3.12+. uv installs the
  right Python by itself; both install the release's pinned versions (a checkout: its
  `uv.lock`).
- **ffmpeg** (`brew install ffmpeg`, `apt install ffmpeg`, `winget install Gyan.FFmpeg`).

## macOS

```sh
./maktaba-node-1.4.0/install-node.sh                       # finds the home on the LAN
./maktaba-node-1.4.0/install-node.sh --home http://192.168.1.20:8080 --name studio-2
./maktaba-node-1.4.0/install-node.sh --instances 2         # + 2 transcription workers
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
files kept), runs `maktaba-pipeline run --node` (passing launchd's SIGTERM on to it), and
turns its exit status into what `KeepAlive` should do: a 77 becomes 0 with a line in
`node.err.log`, so launchd leaves the worker stopped; a 78 or a failure stays non-zero,
so launchd starts it again after 30 s. launchd itself never rotates files, so rotation
happens when a worker (re)starts.

## Linux

```sh
./maktaba-node-1.4.0/install-node.sh --home http://192.168.1.20:8080      # user units
sudo ./maktaba-node-1.4.0/install-node.sh --system --run-as alice         # system units
```

With `--system`, `sudo` resets `PATH`: the installer also looks for uv in the invoking
user's `~/.local/bin` and `~/.cargo/bin`.

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
`~USER/.config/maktaba/node.json`. Run the pipeline command as that account too
(`sudo -u maktaba -H maktaba-pipeline join`): run as root it would read and write root's
node config, which the service never sees.

Every unit has `Restart=on-failure`, `RestartSec=30`, no start limit, and
`RestartPreventExitStatus=77`: a worker the home removed stays down (the unit shows
*failed* with status 77), anything else is started again.

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
powershell -ExecutionPolicy Bypass -File .\maktaba-node-1.4.0\windows\install-node.ps1 -HomeUrl http://192.168.1.20:8080
powershell -ExecutionPolicy Bypass -File .\maktaba-node-1.4.0\windows\install-node.ps1 -Ffmpeg C:\tools\ffmpeg\bin\ffmpeg.exe
```

**Scheduled task (default).** One task per worker, "At log on" of this user (30 s delay so
mapped drives reconnect), running as this user, hidden, with no time limit, normal
priority, and restart when the task fails. It runs `run-node.ps1`, which runs
`maktaba-pipeline run --node` and restarts it itself: after a crash or an unreachable home
in 5 s, doubling to 30 s; after a 78 (not joined yet) every 30 s. It stops for good, with
exit 0 and a line in `node.err.log`, after a clean exit or a 77 (the home removed the
machine), so neither Task Scheduler nor NSSM starts it again.
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
| Node config | `%LOCALAPPDATA%\Maktaba\node.json` (a machine that joined before Epic 38 keeps `%APPDATA%\Maktaba\node.json`) |
| Settings | `%APPDATA%\Maktaba\node.env`, `node@<N>.env` |
| Python environment, scripts | `%LOCALAPPDATA%\Maktaba\node\` |
| Logs | `%LOCALAPPDATA%\Maktaba\Logs\node.log` (+ `node.err.log`), `node@<N>.log` |

```powershell
Get-ScheduledTask -TaskName 'maktaba-node*' | Get-ScheduledTaskInfo         # status
Stop-ScheduledTask -TaskName maktaba-node; Start-ScheduledTask -TaskName maktaba-node   # restart
Get-Content -Wait "$env:LOCALAPPDATA\Maktaba\Logs\node.log"                  # logs
Get-Content -Tail 50 "$env:LOCALAPPDATA\Maktaba\Logs\node.err.log"           # errors + run-node's own lines
& "$env:LOCALAPPDATA\Maktaba\node\venv\Scripts\maktaba-pipeline.exe" join   # the pipeline command
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

## Becoming a full node (Epic 35)

A joined machine can run the whole of Maktaba, not just the pipeline worker: the same
`maktaba-server` as the home, in node mode — its own web UI and API at its own address,
the pipeline worker, and the streaming role (video on demand read from the NAS here; TV
channels only when the home places them here). The home stays the only database, the
only place migrations run and the only holder of secrets. Sign-ins are the home's: open
the machine's web address and press **Continue with <home>**.

Worker-only nodes are converted automatically on their next update once the home's
rollout sends `update.convert` (story 35.12). A worker whose pipeline is too old to know
that command (every worker installed before this release) converts once by hand:

1. Download, from the release the home runs (Admin → System health shows its version),
   the server archive for this machine (`maktaba-server-<version>-<os>-<arch>.zip` or
   `.tar.gz`) plus `checksums.txt`, `checksums.txt.sigstore.json` and `release.json`,
   into one folder, and extract the archive there.
2. Run the conversion from that folder:

   ```sh
   # Linux / macOS
   ./maktaba-server setup --node --from . --python ~/.local/share/maktaba/node/venv/bin/python
   # (or: deploy/packaging/node/install-node.sh --full --server-dir <folder>)
   ```

   ```powershell
   # Windows
   .\maktaba-server.exe setup --node --from . --python "$env:LOCALAPPDATA\Maktaba\node\venv\Scripts\python.exe"
   # (or: windows\install-node.ps1 -Full -ServerDir <folder>)
   ```

`setup --node` keeps `node.json` (identity, key, database credential, home) as it is,
verifies the release in process (the Sigstore bundle of `checksums.txt` with the pinned
release-workflow identity, then every file against `checksums.txt` and `release.json`;
nothing is installed when a check fails), installs it side by side like
`setup --self-managed` (`bin/releases/<version>/`, `current`, `maktaba-launcher`),
keeps this worker's Python environment for the pipeline (`--python`, written to
`[pipeline] python` in `server.toml`), writes `[pipeline] instances` when given
`--instances N`, registers the launcher's service (Windows: the "Maktaba" logon task,
`--mode service` for a Windows service; Linux: a systemd user unit; macOS: a
LaunchAgent), waits until the new service answers as a node, and only then removes the
old worker units (`maktaba-node`, `maktaba-node@N`). `--dry-run` prints the plan.
Without `--from` it fetches the home's version itself (the home's mirror, else GitHub
Releases). A machine joined before the cluster channel's handshake v2 (no
`home.cluster_pub` in `node.json`) must re-join first.

A worker started by hand (a script or a shortcut running `maktaba-pipeline run`) is not
one of the installer's units: stop it yourself after the conversion, or the machine runs
two workers.

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

When the home moves to a new release, run the new release's installer (same scope flags
as before, `--system`). Until then the worker pauses by itself when its pipeline needs
migrations the home has not applied, or when a home migration declares it too old (the
schema guard; the Machines page shows the reason), and resumes once they match:

```sh
./maktaba-node-1.5.0/install-node.sh
```

```powershell
powershell -ExecutionPolicy Bypass -File .\maktaba-node-1.5.0\windows\install-node.ps1
```

From a checkout: `git pull`, then its `deploy/packaging/node/install-node.sh`.

The installer remembers the extras, where the pipeline came from (a release: the archive
you run now decides which; a wheel; a source directory), the account (`--run-as`) and the
Windows mode. It reinstalls the pipeline package (always, even at the same version) with
its pinned dependencies, re-renders the units, and restarts the workers. The node config
is untouched: the machine stays joined. To move a checkout install to releases, pass
`--from-wheel` once, or remove `<node dir>/install.conf` and run the archive's installer.

## Removing

```sh
./maktaba-node-1.4.0/uninstall-node.sh              # or: install-node.sh --uninstall
sudo ./maktaba-node-1.4.0/uninstall-node.sh --system
./maktaba-node-1.4.0/uninstall-node.sh --purge      # also logs and node*.env
```

```powershell
powershell -ExecutionPolicy Bypass -File .\maktaba-node-1.4.0\windows\uninstall-node.ps1 [-Purge]
```

This stops and removes the services and the Python environment. The **node config is
kept**: it is the machine's identity and holds the home's database credentials. To be done
with the home, remove the machine there (Admin → Machines → Revoke, then Remove), then
delete `node.json`. Linux: lingering and the `maktaba` account are left as they are.

## When the home is unreachable, removed, or not joined

The worker's exit status decides what the service does:

| Exit | Meaning | systemd | launchd (`maktaba-node-run`) | Windows (`run-node.ps1`) |
|---|---|---|---|---|
| 0 | stopped | stays stopped | stays stopped | ends; not restarted |
| 77 | the home removed this machine for good: revoked, not a member, database login refused | stays stopped (`RestartPreventExitStatus=77`; unit *failed*, status 77) | logged, runner exits 0: stays stopped | logged, ends with 0: not restarted |
| 78 | not joined to a home yet (`run --node` without a joined node config) | started again in 30 s | started again in 30 s | tried again every 30 s |
| other | home unreachable, crash | started again in 30 s | started again in 30 s | started again in 5 s, doubling to 30 s |

| Situation | What happens |
|---|---|
| The home (its database) is down or not reachable at start: boot before the network, home rebooting | The worker fails its first connect and exits non-zero; the service starts it again (30 s; Windows 5 s doubling to 30 s), with no limit on attempts, until the home answers. |
| The home goes away while the worker runs | The worker keeps running: its heartbeat failures are logged, the database pool reconnects when the home is back. Jobs it had claimed may be handed to other workers by the home's reaper; transcriptions resume from their checkpoints. The cluster channel buffers logs and retries every few seconds up to 30 s. |
| The share is not mounted yet | The worker starts and stops claiming the stages that need the share (its storage check, every 5 min, says why on the home's Machines page); they come back when the share is. |
| The admin revokes or removes the machine | The worker notices at its next heartbeat (within 10 s), or at its next start when its database login is refused, and exits 77: the service leaves it stopped and logs why (launchd: `node.err.log`; Windows: `node.err.log`; systemd: the journal and `systemctl status`). After a reboot or login it starts once, gets 77 again, and stops: no retry loop. To work for the home again, join again (`maktaba-pipeline join`) and restart the service; otherwise uninstall. |
| The machine has not joined (or `node.json` was deleted, or `--no-join`) | `run --node` exits 78 instead of falling back to another database; the service tries again every 30 s, so the worker starts by itself once `maktaba-pipeline join` completes. |
| Drain on the home | The worker finishes what it has and claims nothing new; it stays up. |

## Files

| Path | What |
|---|---|
| `install-node.sh`, `uninstall-node.sh` | macOS + Linux installer (`--help`; `--dry-run` prints the commands and renders the files under a temporary directory) |
| `node_check.py` | "has this machine joined?" (exit 0 / 78): the installers' "join first?" check |
| `node.env.example` | the settings file, all commented |
| `launchd/com.maktaba.node.plist` | LaunchAgent template (`__PLACEHOLDERS__` filled by the installer) |
| `launchd/maktaba-node-run` | the agent's runner: settings, log rotation, `run --node`, exit 77 → 0 for `KeepAlive` |
| `systemd/user/maktaba-node{,@}.service` | user units (main, template for extra workers) |
| `systemd/system/maktaba-node{,@}.service` | system units (`User=` set by a drop-in) |
| `windows/install-node.ps1`, `uninstall-node.ps1` | Windows installer (Windows PowerShell 5.1+) |
| `windows/run-node.ps1`, `node-common.ps1` | the task's / service's supervisor (restarts, 77 / 78), shared helpers (including where the pipeline comes from) |
| `build-release.sh` | builds the release's node assets (wheel, constraints, archives); the release workflow and CI run it. Not in the archive |
| `tests/install-node-dry-run.sh`, `tests/node-origin.Tests.ps1` | the installers' tests: where the pipeline comes from and the commands that install it. Not in the archive |
| `release.conf`, the wheel, the constraints | in a release archive only: written by `build-release.sh` |

## Checks

```sh
plutil -lint deploy/packaging/node/launchd/com.maktaba.node.plist
shellcheck deploy/packaging/node/*.sh deploy/packaging/node/launchd/maktaba-node-run
systemd-analyze verify deploy/packaging/node/systemd/system/*.service      # on Linux
systemd-analyze --user verify deploy/packaging/node/systemd/user/*.service
deploy/packaging/node/install-node.sh --dry-run --instances 2              # renders + lints the plists
MAKTABA_NODE_OS=linux deploy/packaging/node/install-node.sh --dry-run --system   # Linux files, from any host
deploy/packaging/node/tests/install-node-dry-run.sh                        # where the pipeline comes from
deploy/packaging/node/build-release.sh --version v0.0.1.dev1 --out /tmp/node-dist   # the release's assets, locally
```

CI's `node-packaging` gate (`.github/workflows/_node-packaging.yml`) builds those assets
on every PR, installs the wheel into clean uv and pip environments (`--version` must be
the stamped version, `run --help` must list `--node`), runs both installers' tests, and
installs a worker from the archive alone on Linux (`--system`), macOS and Windows.

```powershell
pwsh -NoProfile -File deploy\packaging\node\tests\node-origin.Tests.ps1
foreach ($f in Get-ChildItem deploy\packaging\node\windows\*.ps1) {
    $errors = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$null, [ref]$errors)
    if ($errors) { $errors } else { "$($f.Name): OK" }
}
```

## Packaging: Homebrew and .deb (not done yet)

The archive and its installer are the one way in today. A Homebrew formula or a .deb
would wrap the same installer and the same wheel; neither changes how a node works.

**Homebrew tap formula** (`maktaba-node`, beside the server's
[`homebrew/maktaba.rb`](../homebrew/maktaba.rb), published to the tap by the same
release step). The formula's `url` is the release's `maktaba-node-<version>.tar.gz` with
its SHA-256 (the "Generate Homebrew formula" step already fills these for the server). It
`depends_on "uv"` and `"ffmpeg"`, and installs the archive into `libexec` with a
`bin/maktaba-node-install` wrapper that runs `libexec/install-node.sh "$@"`. It does
**not** build the venv in `install`: Homebrew's sandbox forbids the network and writing to
`~/Library`, and the worker must be a LaunchAgent of the user (not `brew services`, which
would run it without the user's mounted share). So `caveats` tells the user to run
`maktaba-node-install --home ...` once, and again after `brew upgrade` (`post_install`
cannot write the user's `~/Library` either). A `test do` block runs
`install-node.sh --dry-run --no-join` and the wheel's `--version` in a `uv venv`. Work:
the formula template, one more `sed` in the release workflow, a `brew audit` /
`brew test` CI job on macOS. About a day.

**.deb** (`maktaba-node_<version>_all.deb`, built by nfpm like the server's packages; the
pipeline is pure Python, so one package for every architecture). Contents: the archive
under `/usr/lib/maktaba-node/` and a `/usr/sbin/maktaba-node-install` wrapper;
`Depends: python3 (>= 3.12) | uv, ffmpeg`. Debian/Ubuntu releases older than 24.04 have no
Python 3.12, so the wrapper uses uv (which installs its own Python) when present. The
`postinst` runs `install-node.sh --system --no-join --no-start` (it creates the `maktaba`
account and the system units) and prints how to join: joining needs the admin to type a
code on the home, which an unattended `apt install` cannot wait for. The `prerm` runs
`install-node.sh --uninstall --system` on `remove` (not on `upgrade`, so an upgrade
keeps the units and restarts the workers in `postinst`). Building the venv in `postinst`
downloads the dependencies from PyPI; a fully offline .deb would ship a wheelhouse of
every dependency per architecture (`uv pip download` / `pip wheel` against the
constraints for linux amd64 and arm64), which is the larger part of the work. Work: an
`nfpm-node.yaml`, the maintainer scripts, a `package-nfpm` step, and a CI job that
installs the .deb in a Debian container and checks the unit. About two days; the
offline wheelhouse another one or two.
