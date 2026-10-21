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

For each one it reports the config it uses (`MAKTABA_CONFIG`, else the default path), whether the
machine is a **node** (a joined `node.json`) or a **home**, the database (`[database] url`: a
Postgres on this PC or elsewhere, SQLite, or none in node mode), the ports it listens on, the key
files beside its config and its running processes. A script's `set` lines are never shown or
recorded (one may hold a secret). Nothing is changed by finding.

## The setup screen

When something is found, setup's welcome screen offers **"Move the Maktaba this PC runs into the
app"** first, beside "Start fresh" and "Connect to another Maktaba". The move shows its plan
(`maktaba-server adopt --plan <id>` prints the same: what is copied, with sizes, what is carried and
what is not, warnings, and problems that stop it), then runs with progress, then the models download
(the models screen first when the gated model needs a Hugging Face sign-in the move did not bring),
then the app's server starts and the library opens. Each part is one `setup --json --step adopt`
run with `{"action": "find" | "plan" | "move" | "verify", "source": <id>}`
([contract-setup.md](../36-mac-app/contract-setup.md#adopt--move-an-install-this-pc-runs-into-the-app-epic-38-story-3813)).

## What a move does

| # | Step | Notes |
|---|---|---|
| 1 | build the pipeline venv | from the payload, while the old install still runs (minutes the first time) |
| 2 | keep the old tasks' definitions | `Export-ScheduledTask` into `%LOCALAPPDATA%\Maktaba\moved\<name>.xml`, UTF-16 as Task Scheduler reads it back (`schtasks /Query /XML` writes the console's code page under a UTF-16 declaration). Going back re-registers them |
| 3 | stop the old install | `schtasks /End` and `/Disable` on its tasks; its processes get 15 s to exit (an install older than the app has no stop event to ask), then are ended with their trees (its pipeline is a child of its `maktaba-server`); then its ports must be free (web, api, stream, admin, supervisor, pipeline). dizqueTV and anything else not Maktaba's is never touched |
| 4 | copy the files | the keys (`secrets.key`, `stream-signing.key` beside the old config) into `keys\`, `node.json` into `%LOCALAPPDATA%\Maktaba\` (38.1), the search index, the streaming cache, the models, the Hugging Face sign-in, and an SQLite database (with its `-wal` / `-shm`) into the app's data folder. A key or node config already in the app's place and different is never replaced: the move stops and says so |
| 5 | the database | SQLite: the copy above, its `url` the app's. A Postgres elsewhere: its `url` is carried. A Postgres on this PC: it stays where it is, running as it does now, and its `url` is carried (the move into the app's own Postgres comes with the Windows app's Postgres, story 38.10); the plan refuses while it is not running. A node: nothing (it reads the home's database; Epic 37 keeps a copy) |
| 6 | carry the settings | the old `server.toml`'s `[server]` (addresses: the ports other devices and the home know), `[media]` roots and `path_map`, `[transcription]`, `[tv]`, `[pipeline]` (not its `python`), `[cloud]`, `[supervisor]`; never `[server] data_dir` (the app's own) and never a node's own `[database] url`. The script's `set` lines that map to a setting are carried (`MAKTABA_PATH_MAP`, `MAKTABA_WORKER_PATH_MAP`, `MAKTABA_MEDIA_ROOTS`, `MAKTABA_STT_*`, `MAKTABA_PUBLIC_BASE_URL`, `MAKTABA_DISCOVERY_MDNS`); the ones that only located the old binaries (`MAKTABA_API_BIN`, `MAKTABA_STREAMING_BIN`, `MAKTABA_PYTHON`, `MAKTABA_MIGRATIONS_DIR`, `MAKTABA_CONFIG`, `MAKTABA_NODE_CONFIG`, `PYTHON*`) are dropped; any other `MAKTABA_*` line and `serve` flag is listed as not carried. `serve --no-mdns` becomes `[server] mdns = "off"`. A worker-only node gets `[pipeline] instances` = its number of worker tasks. What a fresh setup had collected is dropped |
| 7 | start the app's server | the app registers `\Maktaba Server <SID>` (contract-agent) and runs it, as after `finish` |
| 8 | verify | the app's API answers and `/processes` shows every role running (`maktaba-server adopt --verify`, or the setup step's `verify`); the app's server does it by itself once every role ran healthy for three minutes (the same rule as an update's payload, 38.12) |
| 9 | finish | delete the old tasks (their XML stays in `moved\`). The old folder is **kept**; `moved\move.json` and `maktaba-server adopt` say where it is |

The move is recorded in `%LOCALAPPDATA%\Maktaba\moved\move.json` (`found` → `stopped` → `moved` →
`verified` → `finished`, or `undone`). A step that fails stops the move and says why; running it
again goes on from the last step done (an old install already stopped is not stopped again, its
tasks not exported again). Another install cannot move while one move is under way. Its log is
`logs\adopt.log`.

## This PC (`E:\tv\maktaba-local`)

| | Found | In the app |
|---|---|---|
| task | *Maktaba node (maktaba-local)* → `start-maktaba-node.cmd` (a restart loop under `conhost --headless`) | exported, ended, deleted after verify; `\Maktaba Server <SID>` instead |
| role | full node of the home *Mahmouds-Mac-Studio* (`%APPDATA%\Maktaba\node.json`) | the same node: `node.json` copied to `%LOCALAPPDATA%\Maktaba` (the old one stays for going back) |
| ports | web `0.0.0.0:8780`, api `127.0.0.1:8781`, stream `0.0.0.0:8782`, admin `127.0.0.1:9100` | the same (carried from `server.toml`) |
| media | `Y:/`, `O:/` | the same |
| database | `[database] url` → its own Postgres on 5432 (`pgdata`, `start-postgres.cmd`), not running and unused in node mode | not carried (the node uses the home's, then Epic 37's local copy) |
| keys | `stream-signing.key` beside `server.toml` | `keys\stream-signing.key` |
| pipeline | the checkout's `E:\tv\Maktaba\pipeline\.venv` | the app's venv |
| `serve --no-mdns --bin-dir bin` | `--bin-dir`: the payload's; `--no-mdns`: carried as `[server] mdns = "off"` | |
| kept | `E:\tv\maktaba-local` (logs, `pgdata`, `bin-*`, `mediaintel-snapshot.db`) | untouched |

This PC is converted only after the owner's OK (the user asked for that explicitly).

## Going back

`maktaba-server adopt --undo`: pauses the app's server (the shell's pause flag, so opening the app
does not start it over the old install), stops it and disables `\Maktaba Server <SID>`, re-registers the
old tasks from `moved\<name>.xml` (or turns them back on, before the move finished), and runs them.
What changed in the app after the move stays in the app.
