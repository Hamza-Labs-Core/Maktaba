# Epic 38 — moving an existing Windows install into the app (story 38.13)

> The Windows counterpart of [36.9 migrate-from-dev](../36-mac-app/migrate-from-dev.md). It is part
> of the app's first-run setup, not a runbook: the owner never stops a task, copies a key or edits a
> file by hand.

## What the app finds

`maktaba-server adopt` (and setup's `adopt` step, `{"action": "find"}`, which the welcome screen
asks on Windows) looks for a Maktaba that this PC already runs, in the user's scheduled tasks
(`schtasks /Query /XML ONE`, every task the user can read) and in the running processes (a CIM query:
the `maktaba-*` programs and the pipeline's workers):

| Kind | How it is recognised | Example |
|---|---|---|
| `launcher` | an action runs `maktaba-launcher.exe … serve` (the task `\Maktaba` that `maktaba-server setup --service` writes); its `--root` is the install | a release install |
| `script` | an action runs a `.cmd` / `.bat` (directly, through `cmd /c`, or `conhost --headless cmd /c`) whose lines run `maktaba-server(.exe) serve`; its `cd /d`, `set NAME=VALUE` lines (`%~dp0`, `%VAR%`) and the `serve` flags are read, without running it | this PC: *"Maktaba node (maktaba-local)"* → `E:\tv\maktaba-local\start-maktaba-node.cmd` |
| `node-worker` | the node installer's tasks (`maktaba-node`, `maktaba-node@<n>`, `run-node.ps1`): one install, all its tasks moved together | a worker-only machine |
| `process` | a running `maktaba-server.exe serve` that none of the tasks explains | started by hand |
| `service` | a Windows service whose program is `maktaba-launcher.exe` or `maktaba-server.exe` (`setup --service --system`), from the service list: it runs under the system's account, so its processes have no path the user can read | a LocalSystem install in `C:\ProgramData\Maktaba` |

For each one it reports the config it uses (`MAKTABA_CONFIG`, else the default path), whether the
machine is a **node** (a joined `node.json`) or a **home**, the database (`[database] url`: a
Postgres on this PC or elsewhere, SQLite, or none in node mode), the ports it listens on, the key
files beside its config and its running processes. A script's `set` lines are never shown or
recorded (one may hold a secret). Nothing is changed by finding.

What it cannot read (the tasks, the running programs, the services) fails the find: an install it
would miss could run beside the app. The app's own install, its venv and the setup step's own
process are never counted as another install's, wherever that install's folder is.

An install on the default paths from before Epic 38 is one install whether or not a newer command
copied its `server.toml` and keys from `%APPDATA%\Maktaba` into `%LOCALAPPDATA%\Maktaba` (story
38.1): the copy is read when there is one, the original otherwise. Its node config is the machine's
one file, which the app reads where it is, unless another PC has claimed it (a roaming profile,
38.1's `machine_id`): this PC is then not that node, and the plan says so. Its large state is read where it keeps it
(`Config.StateDirOutsideApp`).

## The setup screen

When something is found, setup's welcome screen offers **"Move the Maktaba this PC runs into the
app"** first, beside "Start fresh" and "Connect to another Maktaba". The move shows its plan
(`maktaba-server adopt --plan <id>` prints the same: what is copied, with sizes, what is carried and
what is not, warnings, and problems that stop it), then runs with progress, then the models download
(the models screen first when the gated model needs a Hugging Face sign-in the move did not bring),
then the app's server starts and the library opens. Each part is one `setup --json --step adopt`
run with `{"action": "find" | "plan" | "move" | "verify", "source": <id>}`
([contract-setup.md](../36-mac-app/contract-setup.md#adopt--move-an-install-this-pc-runs-into-the-app-epic-38-story-3813)).

When an install found is a node of a home, Start fresh is not offered: the app would read the same
node config and run as a second copy of that node. The screen says so, and setup's check and finish refuse it too (`node_here`). A find that
fails shows its error with Try again; it never falls through to a fresh setup. A move recorded part
way is offered to go on, or to restore the old install. A Windows service install is named, and its
plan refuses it: only an administrator can stop it.

A joined node config that no install here runs (one removed, or a move undone, left it) is not in
the way: check warns `node_config_unused`, the welcome screen says a fresh setup makes a new home
and leaves it unused, and finish writes `[server] role = "home"`, so no role of the app ever runs as
that node. A move done can be undone from the welcome screen too. While an install outside the app
is a node of a home, or reads the app's own `server.toml`, setup's status does not count the app as
set up, so the window never registers the app's server over it.

## What a move does

| # | Step | Notes |
|---|---|---|
| 1 | build the pipeline venv | from the payload, while the old install still runs (minutes the first time) |
| 2 | the firewall | the inbound rules that let other devices reach the old install's programs (read only; this PC: TCP and UDP for `maktaba-server` and `maktaba-streaming`, every port, Private and Public) are copied for the app's programs in `server\bin\`: the same protocols, ports and profiles, enabled, inbound, allow, in the group *Maktaba app*. The plan says Windows asks once (`firewall_prompt`); a standard user cannot allow it, and the plan refuses (`firewall_needs_admin`). One elevated run of the payload's `maktaba-server` makes them (one consent prompt, no console window; its input on a file, which it checks: only the app's own rule ids, inbound allow rules for a program beside it), before anything of the old install stops: a No changes nothing. The old install's rules are never changed |
| 3 | keep the old tasks' definitions | `Export-ScheduledTask` into `%LOCALAPPDATA%\Maktaba\moved\<name>.xml`, UTF-16 as Task Scheduler reads it back (`schtasks /Query /XML` writes the console's code page under a UTF-16 declaration). Going back re-registers them |
| 4 | stop the old install | `schtasks /Disable`, then `/End`, on its tasks (off first, so no trigger starts it in between). The cmd.exe (and its conhost.exe) running its script is ended at once, alone, so its restart loop cannot start the server again: the batch file on its command line, resolved against the console's current directory or a `cd /d` before it, must be the script, whole (never `restart.cmd` for `start.cmd`, nor another folder's). Its processes get 15 s to exit, then are ended with their trees. Its ports must be free. Then nothing of it may come back for longer than its script's restart delay (`timeout /t N`, `ping -n N`, `choice /t N`; this PC's waits 10 s, so 15 s). The app's own install and the step's own process are never ended, even inside the old install's folder (a user `setup --service` roots at `%LOCALAPPDATA%\Maktaba`, where the app is installed). A process list that cannot be read stops the move. dizqueTV and anything else not Maktaba's is never touched |
| 5 | copy the files | the keys (`secrets.key`, `stream-signing.key` beside the old config) into `keys\`, the search index, the streaming cache, the models, the Hugging Face sign-in, and an SQLite database (with its `-wal` / `-shm`) into the app's data folder. A key already in the app's place and different is never replaced: the plan refuses it (`key_differs`) before anything is stopped. A node's `node.json` is never copied: the app reads the machine's one where it is, so the app and a restored old install are one node; a node that reads another one is refused (`node_config_elsewhere`). The state comes from where the old install keeps it, is copied into the app's root and kept; a file that is the app's already (a key copied in, an SQLite database in the shared data folder) is never copied onto itself |
| 6 | the database | SQLite: the copy above, its `url` the app's. A Postgres elsewhere: its `url` is carried. A Postgres on this PC: it stays where it is, running as it does now, and its `url` is carried (the move into the app's own Postgres comes with the Windows app's Postgres, story 38.10); the plan refuses while it is not running. A node: nothing (it reads the home's database; Epic 37 keeps a copy) |
| 7 | carry the settings | the old `server.toml`'s `[server]` (addresses: the ports other devices and the home know), `[media]` roots and `path_map`, `[transcription]`, `[tv]`, `[pipeline]` (not its `python`), `[cloud]`, `[supervisor]`; never `[server] data_dir` (the app's own) and never a node's own `[database] url`. The script's `set` lines that map to a setting are carried (`MAKTABA_PATH_MAP`, `MAKTABA_WORKER_PATH_MAP`, `MAKTABA_MEDIA_ROOTS`, `MAKTABA_STT_*`, `MAKTABA_PUBLIC_BASE_URL`, `MAKTABA_DISCOVERY_MDNS`); the ones that only located the old binaries (`MAKTABA_API_BIN`, `MAKTABA_STREAMING_BIN`, `MAKTABA_MIGRATIONS_DIR`, `MAKTABA_CONFIG`, `MAKTABA_NODE_CONFIG`, `PYTHON*`) are dropped; any other `MAKTABA_*` line and `serve` flag is listed as not carried, and so is `MAKTABA_PYTHON` (`app_python`: the app runs its own Python). `serve --no-mdns` becomes `[server] mdns = "off"`. A worker-only node gets `[pipeline] instances` = its number of worker tasks. What a fresh setup had collected is dropped |
| 8 | start the app's server | the app registers `\Maktaba Server <SID>` (contract-agent) and runs it, as after `finish` |
| 9 | verify | the app's API answers and `/processes` shows every role running (`maktaba-server adopt --verify`, or the setup step's `verify`); the app's server does it by itself once every role ran healthy for three minutes (the same rule as an update's payload, 38.12); and the firewall rules the move made are there, enabled, inbound, allowing the app's programs, on the network profile in use (a connection from this PC to its own LAN address never passes the inbound filter, so the rules themselves are read) |
| 10 | finish | delete the old tasks (their XML stays in `moved\`). The old folder is **kept**; `moved\move.json` and `maktaba-server adopt` say where it is |

The move is recorded in `%LOCALAPPDATA%\Maktaba\moved\move.json` (`found` → `stopped` → `moved` →
`verified` → `finished`, or `undone`). A step that fails stops the move and says why; running it
again goes on from the last step done (an old install already stopped is not stopped again, its
tasks not exported again). Another install cannot move while one move is under way. Its log is
`logs\adopt.log`.

## This PC (`E:\tv\maktaba-local`)

| | Found | In the app |
|---|---|---|
| task | *Maktaba node (maktaba-local)* → `start-maktaba-node.cmd` (a restart loop under `conhost --headless`) | exported, ended, deleted after verify; `\Maktaba Server <SID>` instead |
| role | full node of the home *Mahmouds-Mac-Studio* (`%APPDATA%\Maktaba\node.json`) | the same node: `%APPDATA%\Maktaba\node.json` stays where it is and the app reads it |
| ports | web `0.0.0.0:8780`, api `127.0.0.1:8781`, stream `0.0.0.0:8782`, admin `127.0.0.1:9100` | the same (carried from `server.toml`) |
| firewall | inbound allow rules, TCP and UDP, every port, Private and Public, for `maktaba-server` and `maktaba-streaming` in the old folder | the same for the app's `server\bin\` programs (*Maktaba app*), made with one consent prompt; the old rules untouched |
| media | `Y:/`, `O:/` | the same |
| database | `[database] url` → its own Postgres on 5432 (`pgdata`, `start-postgres.cmd`), not running and unused in node mode | not carried (the node uses the home's, then Epic 37's local copy) |
| keys | `stream-signing.key` beside `server.toml` | `keys\stream-signing.key` |
| pipeline | the checkout's `E:\tv\Maktaba\pipeline\.venv` | the app's venv |
| `serve --no-mdns --bin-dir bin` | `--bin-dir`: the payload's; `--no-mdns`: carried as `[server] mdns = "off"` | |
| kept | `E:\tv\maktaba-local` (logs, `pgdata`, `bin-*`, `mediaintel-snapshot.db`) | untouched |

This PC is converted only after the owner's OK (the user asked for that explicitly).

## Going back

The setup screen's **Restore the old install** (beside Try again when a move stops, and on the
welcome screen for a move recorded part way or done) and `maktaba-server adopt --undo` do the same. They
pause the app's server for this sign-in, stop it and disable this user's app tasks
(`\Maktaba Server <SID>`, and a task of the old shared name `\Maktaba Server` that runs as this
user), and take the window out of server mode (`%LOCALAPPDATA%\com.hamzalabs.maktaba\mode.json`).
They turn the old tasks on again (registered again from `moved\<name>.xml` when the move finished;
the definitions are in the record before the stop, so a stop that failed part way is undone too),
and run them unless the old install still runs. After this sign-in the disabled task keeps the
app's server off, and the window opens setup: its status step does not count the app as set up
while the move is undone, so the welcome screen offers the move again. What changed in the app after
the move stays in the app.

They remove the firewall rules the move made (one consent prompt); the old install's own rules were
never touched and serve it again. A rule Undo cannot remove is recorded as `firewall_left` in
`move.json`: it only allows the app's programs, which no longer run.

Every finding of the plan and every refusal carries a code and its values (contract-setup), which
the screens put into words in English or Arabic: findings `space_low`, `worker_not_joined`,
`postgres_down`, `key_differs` (`path`, `from`), `service_install`, `firewall_needs_admin`,
`firewall_unknown`, `node_config_elsewhere` (problems), `postgres_stays`, `started_by_hand`,
`firewall_prompt` (warnings), `app_python` (not carried); refusals
`adopt_moved_already`, `adopt_under_way`, `adopt_task`, `adopt_still_running`, `adopt_port_busy`
(`port`), `adopt_key_differs`, `adopt_processes_unknown`, `adopt_firewall_declined`,
`adopt_firewall`, `interrupted`, `adopt_failed`; `node_here`, `install_unknown` from check and
finish (Start fresh beside a node); and `node_config_unused`, check's warning for a node config no
install here runs.
