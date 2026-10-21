# Epic 38 — moving an existing Windows install into the app (story 38.13)

> The Windows counterpart of [36.9 migrate-from-dev](../36-mac-app/migrate-from-dev.md). It is part
> of the app's first-run setup, not a runbook: the owner never stops a task, copies a key or edits a
> file by hand.

## What the app finds

`maktaba-server adopt --find` (and setup's `check` step, which includes its answer) looks for a
Maktaba that this PC already runs, in the user's scheduled tasks (`schtasks /Query /XML`, every task
the user can read) and in the running processes:

| Kind | How it is recognised | Example |
|---|---|---|
| `launcher` | an action runs `maktaba-launcher.exe … serve` (the task `\Maktaba` that `maktaba-server setup --service` writes); its `--root` is the install | a release install |
| `script` | an action runs a `.cmd` / `.bat` (directly, through `cmd /c`, or `conhost --headless cmd /c`) whose lines run `maktaba-server(.exe) serve`; its `cd /d`, `set NAME=VALUE` lines and the `serve` flags are read, without running it | this PC: *"Maktaba node (maktaba-local)"* → `E:\tv\maktaba-local\start-maktaba-node.cmd` |
| `node-worker` | the node installer's tasks (`maktaba-node`, `maktaba-node@<n>`, `run-node.ps1`) | a worker-only machine |
| `process` | a running `maktaba-server.exe serve` that none of the tasks explains | started by hand |

For each one it reports the config it uses (`MAKTABA_CONFIG`, else the default path), whether the
machine is a **node** (a joined `node.json`) or a **home**, the database (`[database] url`: a
Postgres of its own, SQLite, or none in node mode), the ports it listens on, the key files beside
its config, and what will not be carried (below). Nothing is changed by `--find`.

## The setup screen

When something is found, setup's first screen offers **"Move the Maktaba this PC runs into the app"**
(the default) beside "Start fresh" and "Connect to another Maktaba". The move shows its plan (the
`--dry-run` output, with sizes), then runs with progress, then opens the library. It is one
`setup --json --step adopt` run with `{"action": "plan" | "move" | "verify", "source": <id>}`.

## What a move does

| # | Step | Notes |
|---|---|---|
| 1 | build the pipeline venv | from the payload, while the old install still runs (minutes the first time) |
| 2 | export the old task | `schtasks /Query /TN <name> /XML` into `%LOCALAPPDATA%\Maktaba\moved\<name>.xml` (going back re-registers it) |
| 3 | dump a home's database | a home with a Postgres of its own: `pg_dump -Fc` of the running database into `backups\from-<kind>-<time>.dump`, as 36.9 does (the joined machines' roles go `NOLOGIN` first). A node has no database of its own to carry (its copy of the home's database is rebuilt by Epic 37); SQLite is copied as a file after step 4 |
| 4 | stop the old install | `schtasks /End` and `/Disable` on its task, then wait until its processes are gone and its ports are free; a process left over whose image is in the old folder is ended. dizqueTV and anything else not Maktaba's is never touched |
| 5 | restore | the dump into the bundled Postgres (`--role=maktaba --no-owner`), the counts checked as in 36.9 |
| 6 | carry the settings | the old `server.toml`'s `[server]` addresses (the ports other devices and the home know), `[media]` roots and `path_map`, `[transcription]`, `[tv]`, `[pipeline]`, `[cloud]`; never `[server] data_dir` (the app's own) and never a `[database] url` that pointed at the Postgres just replaced. The script's `set` lines that map to a setting are carried (`MAKTABA_PATH_MAP` → `[media] path_map`, `MAKTABA_STT_*` → `[transcription]`); the ones that only located the old binaries (`MAKTABA_API_BIN`, `MAKTABA_STREAMING_BIN`, `MAKTABA_PYTHON`, `MAKTABA_MIGRATIONS_DIR`, `MAKTABA_CONFIG`) are dropped |
| 7 | carry the keys | `secrets.key`, `stream-signing.key` (beside the old config) into `keys\`; `node.json` into `%LOCALAPPDATA%\Maktaba\` (38.1). A key already in the app's place is never replaced |
| 8 | start the app's server | register `\Maktaba Server` (contract-agent) and run it |
| 9 | verify | every role running in `/processes`; a home: the library and transcript counts match the dump's; a node: the home sees this machine checked in (its `last_seen` moves) |
| 10 | finish | delete the old task (its XML stays in `moved\`). The old folder is **kept**: its path is shown in Settings › Machine, with its size |

The move is recorded in `%LOCALAPPDATA%\Maktaba\moved\move.json` (`found` → `stopped` → `moved` →
`verified` → `finished`). A step that fails stops the move and says why; running it again goes on
from the last step done. Its log is `logs\adopt.log`.

## This PC (`E:\tv\maktaba-local`)

| | Found | In the app |
|---|---|---|
| task | *Maktaba node (maktaba-local)* → `start-maktaba-node.cmd` (a restart loop under `conhost --headless`) | exported, ended, deleted after verify; `\Maktaba Server` instead |
| role | full node of the home *Mahmouds-Mac-Studio* (`%APPDATA%\Maktaba\node.json`) | the same node: `node.json` moved to `%LOCALAPPDATA%\Maktaba` |
| ports | web `0.0.0.0:8780`, api `127.0.0.1:8781`, stream `0.0.0.0:8782`, admin `127.0.0.1:9100` | the same (carried from `server.toml`) |
| media | `Y:/`, `O:/` | the same |
| database | `[database] url` → its own Postgres on 5432 (`pgdata`, `start-postgres.cmd`), not running and unused in node mode | not carried (the node uses the home's, then Epic 37's local copy) |
| keys | `stream-signing.key` beside `server.toml` | `keys\stream-signing.key` |
| pipeline | the checkout's `E:\tv\Maktaba\pipeline\.venv` | the app's venv |
| `serve --no-mdns --bin-dir bin` | `--bin-dir`: the payload's; `--no-mdns`: carried as `[server] mdns = "off"` | |
| kept | `E:\tv\maktaba-local` (logs, `pgdata`, `bin-*`, `mediaintel-snapshot.db`) | untouched |

This PC is converted only after the owner's OK (the user asked for that explicitly).

## Going back

Until the old folder is removed, `maktaba-server adopt --undo`: stops the app's server and disables
`\Maktaba Server`, re-registers the old task from `moved\<name>.xml`, and runs it. What changed in the
app after the move stays in the app's database.
