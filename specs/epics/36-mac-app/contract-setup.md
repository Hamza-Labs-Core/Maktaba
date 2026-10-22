# Contract — `maktaba-server setup --json` (Epic 36, story 36.6)

> **Owner of this contract:** track C (the app shell asks for it). **Implements it:**
> `cmd/maktaba-server` (`cmd_setup_json.go`, `setup_*.go`; every step since Epic 38 story 38.6,
> for the Mac, Windows and Linux alike). **Consumer:** `apps/desktop/src-tauri/src/setup.rs`, which
> runs it and relays it to the setup screens in `web/src/pages/Setup/`.

The CLI wizard and the app's setup screens share one implementation: every screen calls one
**step**. A step is one process run:

```
<Resources>/server/bin/maktaba-server setup --json --step <name>
```

- **Input:** one JSON object on **stdin** (then stdin is closed). Secrets (passwords, tokens) travel
  only here, never in argv (argv is visible to every user through `ps`) and never in the environment.
- **Output:** **NDJSON on stdout**, one event per line. Any number of `progress` lines, then exactly
  one final `result` or `error` line. stderr is free-form diagnostics; the app keeps the last 50
  lines for the "details" box and never parses it.
- **Exit code:** 0 with a `result`, 1 with an `error`, 2 for usage (unknown step, bad JSON).
- **Idempotent:** running a step again with the same input is safe (the user may go back a screen).
  Each step saves what it collected in the pending setup state (`<state>/state/setup.json`, 0600);
  `finish` applies it and removes it. The pending state holds no secret in the clear: the admin's
  password only as its argon2id hash, the Hugging Face token where the pipeline reads it
  (`<models>/huggingface/token`, 0600), the Plex account token in `keys/plex-setup.token` (0600)
  until `finish` hands it over and deletes it.
- **Never echo secrets** in any line on stdout or stderr, including in `error.message`.
- **The app's window (Windows):** the app passes its main window's handle in `MAKTABA_SETUP_HWND`
  (a number, no secret). A step that asks Windows for an administrator's consent (the move's
  firewall rules) makes that window the prompt's owner, so the prompt opens in front of the app.
  Without it (the CLI) the prompt has no owner, as before.

### Events

```jsonc
{"type":"progress","stage":"download","message":"Downloading whisper-large-v3-turbo","done":123,"total":456}
{"type":"result","data":{ /* step-specific */ }}
{"type":"error","code":"root_missing","message":"Y:\\Lectures is not a folder on this machine.","field":"roots","params":{"path":"Y:\\Lectures"}}
```

| Field | Meaning |
|---|---|
| `progress.stage` | short machine name of the current phase (`download`, `verify`, `venv`, …) |
| `progress.message` | one plain-English line for the screen |
| `progress.done` / `progress.total` | optional; bytes or items; `total` 0/absent → indeterminate bar |
| `error.code` | machine code; the screens show their own text for it (`setup.error.<code>` in English and Arabic, web/src/i18n), and `message` only for a code they do not know |
| `error.message` | English (a log, the terminal setup, an app older than the code); the screens put it under Details, never beside their own text |
| `error.params` | optional; the values the code's text takes (`path`, `line`, `min`, `max`, `model`, …); a size is in bytes under a `*_bytes` name |
| `error.field` | optional; the input field the error belongs to (the screen highlights it) |

The codes are a contract: the app's screens and the terminal setup (Epic 39 story 39.6) key their
texts on them, so a code keeps its meaning and its params once shipped (a new reason is a new code).

## Steps (in screen order)

### `status`
Input `{}`. Is this Mac already set up? The app calls it before showing setup: when `configured`,
it registers the login item again and opens `web_url` instead of a second setup (not after the user
chose "Connect to another Maktaba…"). Not `configured` while the move recorded in
`<state>/moved/move.json` is `undone` (`maktaba-server adopt --undo`, below): the old install runs
the machine's Maktaba again, and the app must not register its server over it. A `finish` (setup
afresh) or a new move ends that. Nor while an install outside the app is here that is a node of a
home, or reads the app's own `server.toml` (an install on the default paths from before Epic 38,
whose config and keys a newer command copied into `%LOCALAPPDATA%\Maktaba`, story 38.1): the
welcome screen offers its move instead.
```json
{"configured": false, "web_url": "http://127.0.0.1:8088", "supervisor_url": "http://127.0.0.1:9103"}
```

### `check` — what will run + disk/RAM
Input `{}`.
```json
{
  "platform": "darwin", "arch": "arm64",
  "ram_bytes": 103079215104, "disk_free_bytes": 812345678901,
  "data_dir": "/Users/me/Library/Application Support/Maktaba",
  "need": {"ram_bytes": 17179869184, "disk_bytes": 21474836480},
  "ok": true,
  "problems": [],
  "warnings": [{"code": "transcribe_cpu", "message": "No NVIDIA GPU: transcription runs on the processor, many times slower than on a GPU."}],
  "gpus": [],
  "second_listen": {"runs": true},
  "runs": [
    {"role": "postgres", "what": "The library database (private to Maktaba)"},
    {"role": "api", "what": "The server the app and your devices talk to"},
    {"role": "streaming", "what": "Playback and Live TV"},
    {"role": "pipeline", "what": "Indexing, transcription (2 workers) and search", "params": {"workers": 2}}
  ]
}
```
Each finding is `{"code", "params"?, "message"}`, shown as `setup.check.note.<code>` (the English
`message` for a code the app does not know). `problems` block the Next button (`ok` is false while
there is one): `platform_intel_mac`, `platform_unsupported` (`os`, `arch`), `disk_low` (`free_bytes`,
`need_bytes`, `dir`), `ram_low` (`have_bytes`, `need_bytes`). `warnings` do not: `disk_tight`
(`free_bytes`, `comfortable_bytes`), `ram_tight` (`need_bytes`), `transcribe_cpu` (Windows and Linux
without an NVIDIA GPU: transcription runs on the processor), `node_config_other_pc` (`path`, `owner`;
Windows: the roaming profile's node settings belong to another PC, so this machine is no home's
machine until it is added as its own, Epic 38 story 38.1). `gpus` lists the NVIDIA GPUs
(`nvidia-smi`; `name`, `memory_mb`, and where nvidia-smi says them `compute_cap` and `driver`), and
the pipeline's run line names the one it transcribes on (`params.gpu`).

With the second listen on, `second_listen` says whether this machine can run it: Apple Silicon (MLX),
or an NVIDIA GPU with 9.5 GiB on Windows (7.5 GiB elsewhere), a compute capability of 7.5 (Turing) or
more and an R580 driver or newer, which the pipeline's room check and the supervisor's tiers use
too. Where it cannot, `{"runs": false, "code", "params"}` names why, and the same finding is among
the `warnings`: `second_listen_no_gpu`, `second_listen_gpu_small` (`gpu`, `have_bytes`,
`need_bytes`), `second_listen_gpu_old` (`gpu`, `compute`), `second_listen_driver_old` (`gpu`,
`driver`, `need`), `second_listen_intel_mac`. A value nvidia-smi does not report is not held against
the GPU. The models step then asks for no Hugging Face sign-in (for a model that would never run),
though the NAS copy can still be chosen there, and Whisper's words are kept. `runs` lines are `setup.check.run.<role>` (the pipeline by its `workers`: `pipeline`,
`pipelineOne`, `pipelineWorkers`, then `onGpu`). A platform adds its own findings through
`platformChecks` (setup_steps.go; Linux's glibc, systemd user manager and linger come with Epic 39).

### `admin` — the first account
Input `{"username": "admin", "password": "…"}`. Errors, with `field`: `password_short` (`min`: 8),
`password_long` (`max`: 256 bytes), `password_newline` (the api reads the password as one line),
`username_length` (`max`: 64), `username_space` (no leading or trailing space), `username_control`
(no control character).
Result `{"username": "admin"}`. The password is hashed at once by `maktaba-api hash-password` (on
its stdin; the api's own argon2id), and only the hash waits for `finish`.

### `libraries` — library folders and the NAS path map
Input:
```json
{"roots": ["/Volumes/Media/Lectures"],
 "path_map": [{"from": "/mnt/media", "to": "/Volumes/Media"}]}
```
`path_map` rewrites paths other machines report (`MAKTABA_PATH_MAP`); it may be empty. It is saved
in the map's JSON form (`{"/mnt/media": "/Volumes/Media"}`), which no path's characters break (a
Windows drive letter, a UNC path). A root must be a whole path of this machine (a bare drive `Y:` is
`Y:\`; a relative one would resolve against wherever the server runs); roots are cleaned (`Y:/`
becomes `Y:` plus a backslash on Windows) and kept once each, two spellings being one folder where
the disk ignores case (Windows, the Mac) and two on Linux. Both sides of a path-map line are whole
paths: the left one in its machine's form (`/…`, `X:…`, `\\…`), the right one this machine's.
Result: `{"roots": [{"path": "/Volumes/Media/Lectures", "exists": true, "readable": true}]}`.
Errors, with `field: "roots"`: `root_missing` / `root_unreadable` / `root_relative` (`path`),
`roots_none`; with `field: "path_map"`: `bad_path_map` (`line`: a line without both paths),
`path_map_relative` (`line`, `path`).

### `models` — transcription models
Input, one of:
```json
{"source": "huggingface", "hf_token": "hf_…"}
{"source": "nas", "path": "/Volumes/Models/huggingface"}
```
The Cohere second-listen model is gated: with `huggingface` the token is checked (Hugging Face's
`whoami-v2`, then a `HEAD` of the model's `config.json` with it) and saved where the pipeline's
Hugging Face client reads it (`<models>/huggingface/token`, mode 0600). With `nas` the home's
published copy is found (`<path>/<org>__<name>/<revision>/manifest.json`, `path` being the share's
`maktaba/models` folder or the share itself). Errors: `hf_token_invalid`, `hf_gated` (the user must
accept the model's terms on huggingface.co; `model`), `hf_token_empty`, `nas_path_empty`,
`nas_missing` (`path`, `model`), `network`. With the second listen off
(`[transcription] refine = "off"`), or on a machine that cannot run it (`check`'s `second_listen`), no
token is needed and the gated model is not checked (a NAS copy is any model the home published
there); the download then takes only Whisper and the search embedder (`--only whisper,embedder`).

The download itself needs the venv, so it is a second call, made by the install screen after the
`venv` step: input `{"action": "download"}` runs the venv's `maktaba-pipeline models download
--json` (with `--share <path>` for the NAS source), streaming `progress` with `stage: "download"`,
byte `done`/`total`. Result `{"models": {"whisper": "ready", …}}`. Errors: `venv_missing`,
`hf_gated` (exit 3 of the download), `download_failed`, `interrupted` (it resumes next time).

### `plex` — optional Plex sign-in (PIN flow; the Plex password never reaches Maktaba)
Input `{"action": "skip"}` → result `{"linked": false}` (a sign-in made before is forgotten).
Input `{"action": "pin"}` → result `{"pin_id": 123, "code": "ABCD", "link_url": "https://app.plex.tv/auth#?clientID=…&code=ABCD…"}`;
the screen shows the code and opens `link_url` in the browser (the code is filled in there). The
plex.tv client id is this install's, kept for every later sign-in.
Input `{"action": "poll", "pin_id": 123}` → result `{"linked": true}` once the user signed in (the
account token is saved in `keys/plex-setup.token`), `{"linked": false}` while waiting; errors
`pin_expired`, `network`. `finish` hands the token to `maktaba-api plex-link`, which stores the Plex
server the account reaches (the one on this network first) as the Settings sign-in does, and
deletes it.

### `venv` — the pipeline's Python environment (story 36.3)
Input `{}`. Builds (or checks: `--check` first, an up-to-date venv is `reused`) the pipeline venv
with the payload's Python and wheel (`MAKTABA_APP_BUNDLE`, else the payload holding this
`maktaba-server`) and streams its progress as `progress` events, `stage: "venv"`, `done` out of
`total: 1000` (the bootstrap's overall share, never going back). Result
`{"python": "3.12.x", "pipeline": "<version>", "reused": false}`. Errors: `venv_failed` (the message
has the failing step's last lines and the full log, `<logs>/venv.log`), `server_missing` (not run
from the app), `interrupted`. Track A relays track B's bootstrap here, so the app depends on this
one contract only.

### `llm` — the language model of the LLM stages
Input `{}`, or `{"models": ["qwen2.5:7b"], "check_only": true}`. `models` defaults to `[llm] models`
(`qwen2.5:7b`, what `check_tags`, `check_clips` and enrich use); `check_only` reports what is
missing and the space it needs without downloading. The step uses the running supervisor's model
server (its `GET /llm`), else `[llm] url`, else starts the bundled Ollama
(`Resources/server/ollama/ollama serve`) on a free loopback port for the while, with the
install's `models/ollama`. Progress: `stage: "start"` (that server starting), `"check"` (the space
needed, from the registry's manifest, and the space free), then `"download"` with byte
`done`/`total` per model. A download that stopped (the app closed, the network dropped) resumes
where it was the next time. Running it again with everything there downloads nothing.
```json
{"url": "http://127.0.0.1:11533", "bundled": true,
 "models": [{"name": "qwen2.5:7b", "ready": true, "downloaded": true, "size_bytes": 4683087332}],
 "disk_needed_bytes": 4683087332, "disk_free_bytes": 812345678901}
```
`disk_free_bytes` is -1 for a server on another machine. Errors: `disk_full` (the message says how
much is needed and free; the app stops setup), `llm_unavailable` (no bundled Ollama and no
`[llm] url`), `llm_failed` (anything else: the network, the registry). The app runs it after
`venv` and leaves any error but `disk_full` to the server, which downloads a missing model in the
background (`[llm] pull = "auto"`). `maktaba-server models llm` is the same for a terminal.

### `finish`
Input `{}`. Writes `server.toml` (the libraries and the path map; ports another program holds move
to the fallback range), creates the database (the bundled Postgres: initdb on a first run, a
short-lived server of its own; a Postgres `[database] url`; or, outside the app, the SQLite file,
its folder made), runs the migrations
(`maktaba-api migrate up`), seeds the admin account from its hash (`maktaba-api adduser
--password-hash-file`, through a 0600 file removed after; an admin already there, exit 3, is not an
error), and, after a Plex sign-in, stores the Plex server (`maktaba-api plex-link`). Result
`{"web_url": "http://127.0.0.1:8088", "supervisor_url": "…", "plex": {"linked": true, "server":
"…"}}` (Plex is optional: a failure there is in the result, not an error). Errors: `admin_missing`,
`database_failed`, `database_unsupported` (`scheme`), `migrate_failed`, `admin_failed`. After it the
app registers the login item (which starts the server), waits for `/processes` to show every role
running, and opens `web_url`.

### `adopt` — move an install this PC runs into the app (Epic 38 story 38.13)
Instead of `check` … `finish` when the PC already runs Maktaba outside the app
([move-an-install.md](../38-windows-app/move-an-install.md)).

- `{"action": "find"}` → `{"sources": [<source>], "move": <record>}`: what runs (`id` — the task's
  name, `pid:<n>` or `service:<name>` —, `kind` `launcher` / `script` / `node-worker` / `process` /
  `service`, `dir`, `config`, `db` `node` / `sqlite` / `postgres` / `own-postgres` with `db_where`
  (empty for a service), `home` for a node, `service`, `ports`, `pids`; never a script's
  environment) and the move recorded in `<state>/moved/move.json`, if any. The tasks, the processes
  and the services must all be readable, or the step fails (`adopt_find`): an install it would miss
  could run beside the app. The welcome screen asks on Windows only (elsewhere nothing is found); a
  failed find is shown with Try again, never taken as "nothing runs". A recorded move at `found` or
  `stopped` (one that stopped part way) is offered to go on, or to be undone; one at `moved`,
  `verified` or `finished` to be undone (the welcome screen shows when the window opens setup, as
  after "Connect to another Maktaba…"). `leftover_node` (`home`, `path`) is a joined node config that
  no install found runs (below).
- `{"action": "plan", "source": id}` → `{"source", "items": [{"code", "params"?, "what", "from",
  "to", "size", "done"}], "carried", "not_carried", "problems", "warnings", "firewall", "need",
  "free"}`; read
  only. A move with `problems` is refused. An install on the default paths from before Epic 38 is
  one install whether or not a newer command copied its `server.toml` and keys from
  `%APPDATA%\Maktaba` into the app's place (the copy wins, as on its own reads; the original
  otherwise; its node config is the one file the roles read, `NodeConfigPath`); its state is read with its own rule (`Config.StateDirOutsideApp`: a default data
  folder that holds models, caches or the index is its state root), copied beside it into the
  app's root and kept where it was; what is the app's file already (a key copied in, an SQLite
  database in the shared data folder) is never copied onto itself. Items are
  `setup.move.item.<code>` (`key` with `name`, `search_index`, `streaming_cache`, `models`,
  `database`, `database_wal`, `database_shm`, `hf_token`). A node's `node.json` is never copied: one
  per machine (story 38.1), read where it is by the app and by the old install on Undo alike. Every other entry is `{"code", "params"?, "message"}`, shown as its
  own text (the English `message` for a code the app does not know): `carried` as
  `setup.move.carry.<code>` (`section` `name`, `database_url`, `pipeline_instances` `count`, `env`
  `name`, `no_mdns`), `not_carried` as `setup.move.nocarry.<code>` (`pipeline_python`, `app_python`
  `name`: the script's `MAKTABA_PYTHON`, `env` `name`, `serve_flag` `flag`, `firewall_rule`
  `rule`, `program`: a firewall rule of a program the app has none of), `problems` and
  `warnings` as `setup.move.note.<code>`: problems `space_low`
  (`need_bytes`, `free_bytes`), `worker_not_joined` (`path`), `postgres_down` (`where`),
  `key_differs` (`path`, `from`: the app holds another key there, which is never replaced; found
  in the plan, before anything is stopped), `node_config_elsewhere` (`path`, `app`: the node reads
  another node config than the one the app reads, so the app would not be that node),
  `service_install` (`name`: a Windows
  service, run by the system's account), `firewall_needs_admin` (`count`), `firewall_unknown`,
  `node_config_other_pc` (`path`, `owner`) for a node installer's worker with no node config of its
  own; warnings `postgres_stays` (`where`), `started_by_hand`, `firewall_prompt` (`count`),
  `firewall_blocked` (`rule`, `program`: a block rule on an app's program beats the copies, as a
  block applies before any allow), `node_config_other_pc` (`path`, `owner`: the roaming profile's
  node config is another PC's, so the app on this PC is not that node). `firewall` is
  the inbound rules the app's programs get: one copy of each enabled inbound allow rule bound to
  one of the old install's programs (in its folder, or its server and running programs elsewhere)
  whose program the app's payload has (`bin\<the same name>`),
  the same protocol, ports and profiles, none the app has already. Making them needs an
  administrator: the plan says Windows asks once (`firewall_prompt`), or refuses when this user
  cannot allow it (a standard user).
- `{"action": "move", "source": id}` → `{"move": <record>, "web_url", "supervisor_url"}`, with
  `progress` events whose `stage` the screens say in their own words (`setup.move.progress.<stage>`:
  `venv`, as the venv step's, then `firewall`, `stop`, `copy`, `settings`; the models download's
  `download`, the `llm` step's `start` / `check` / `download`). After the venv and before anything of
  the old install stops, the plan's firewall rules are made in one elevated run of the payload's
  `maktaba-server` (one consent prompt, no console window; its input on a file, which it checks: only
  the app's own rule ids, inbound allow rules for a program beside it); a No changes nothing
  (`adopt_firewall_declined`), nor does a prompt not answered within three minutes
  (`adopt_firewall_timeout`: the change is withdrawn, so a run a late Yes starts makes nothing).
  The old install is stopped (its tasks disabled, then ended, the
  cmd.exe running its script ended at once, its processes given their grace, its ports free, then
  nothing of it back for longer than its script's restart delay), its files copied,
  `server.toml` written; what a fresh setup collected is dropped. The app's own install, its venv
  and the step's own process are never stopped, wherever the old install's folder is. Run again
  after a failure it goes on from its last step. The app then runs `models` `{"action": "download"}`
  (on `hf_gated` the models screen first), `llm` as a fresh install does (only `disk_full` stops
  it), and registers the login item, as after `finish`.
- `{"action": "undo"}` → `{"move": <record>}`: the old install runs again, as below; also for a move
  that stopped part way (the screen's "Restore the old install", beside Try again). A `firewall`
  progress event comes before Windows asks to remove the move's rules.
- `{"action": "verify", "wait_sec": n}` → `{"move": <record>}`: the app's API answers and every
  process runs, a program of the app listens beyond loopback on each of the old install's web, API
  and streaming addresses that other devices reached, as its `server.toml` carried them
  (`0.0.0.0:8780`, say: read from Windows' TCP table), and the firewall rules the move made are there, on, inbound, allowing the app's
  programs, on the network profile in use, with no block rule of those programs beating them
  there (a connection from this PC to its own LAN address never
  passes the inbound filter, so the rules themselves are read); then the old task is removed. The
  server does the same by itself once every role ran healthy for three minutes, so the screens need
  not wait for it.

Errors: `adopt_find`, `adopt_gone` (field `source`), `adopt_blocked` (the plan's problems), and
the move's own reasons, each with its params: `adopt_moved_already` / `adopt_under_way`
(`source`), `adopt_task` (`task`: Task Scheduler refused to read or turn off an old task),
`adopt_still_running` (`pids`), `adopt_port_busy` (`port`), `adopt_key_differs` (`path`, `from`: a
key that turned different after the plan), `adopt_processes_unknown` (the running programs cannot
be listed: not sure is not "stopped"), `adopt_firewall_declined` / `adopt_firewall_timeout`
(`minutes`) / `adopt_firewall` (the app's firewall rules not made; nothing changed); `interrupted` (a stop: the next run goes on), `adopt_failed`
for anything else, `adopt_verify`, `adopt_no_move` and `adopt_undo` (undo), and the venv step's.

`check` (a problem: Next is blocked) and `finish` (an error, before anything is written) refuse a
fresh setup beside a node: `node_here` (`home`, `source`) when an install outside the app is a node
of a home, its task enabled or not, running or not (the app would read the same node config, or the
copy an undone move left, and run as a second copy of that node), `install_unknown` when what runs
cannot be looked at. The terminal setup of that very install (its own `maktaba-server`) is not a
second copy. The welcome screen does not offer Start fresh there, and says why. A joined node
config at the place the app's roles read it (`%LOCALAPPDATA%\Maktaba\node.json`, else the roaming
one) that no install here runs, left by an install removed or a move undone, is not in the way:
`check` warns `node_config_unused` (`home`, `path`), the welcome screen says a fresh setup makes a
new home and leaves it unused, and `finish` writes `[server] role = "home"`, so no role of the app
ever runs as that node.

Undo (the step's `undo`, or `maktaba-server adopt --undo` in a terminal) runs the old install again:
the pause flag, the app's server stopped, this user's app tasks disabled (`\Maktaba Server <SID>`,
and a task of the old shared name `\Maktaba Server` that runs as this user), the window's
`mode.json` out of server mode (Windows: `%LOCALAPPDATA%\com.hamzalabs.maktaba\mode.json`; a client
mode stays), the old tasks registered again from their XML (or turned on again) and run, unless the
old install still runs, the firewall rules the move made removed (one prompt; the old install's own
rules were never touched, and a rule Undo cannot remove is recorded as `firewall_left`), the record
`undone`. The pause flag ends with the sign-in; what keeps the
app's server from starting over the old install after it is the disabled task (the window starts
only an enabled one) and `status` above: the window opens setup, whose welcome screen finds the old
install and offers the move again.

### Stopping a step

A stop (the app quitting) cancels the step, which asks the program it runs to stop: the venv
bootstrap and the models download get SIGTERM (Windows: CTRL_BREAK to the process group each starts
in, which the bootstrap takes as SIGBREAK) and up to 45 s before they are killed, so the bootstrap
stops pip and records where it was. The step then ends with `interrupted`, and the next run goes on.

### The terminal

`maktaba-server setup` (an install outside the desktop app: a release, a package) asks the same
steps in the terminal, in this process: `check`, `admin`, `libraries`, `plex`, `finish`, with only
two questions of its own before them (where the data goes, and the database: SQLite or a Postgres
url; the app runs its own Postgres). A refused answer is asked again with the step's message.
Epic 39's terminal setup (story 39.6) does the same from an app payload, in English or Arabic, by
the codes above.

## The screens' words

The shell's `app_state` says the platform (`"macos"`, `"windows"`, `"linux"`); the screens say "this
Mac" / "this PC" / "this computer" and "the menu bar" / "the tray" from it (Epic 38 story 38.6).
On Windows a turned-off login item is the sign-in task disabled in Task Scheduler: the screen's one
button turns it back on (the app does it), where a Mac's user does it in System Settings.

## Not in the contract

- The supervisor's state is the existing `GET http://127.0.0.1:9103/processes` (`supervisor.Snapshot`).
  With a language model server it has an `llm` field (`llmserver.Status`: `url`, `bundled`, `up`,
  `ready`, `models[]` with `state` ready / downloading / missing / failed / unknown and byte
  `done`/`total`), which the menu bar shows; the `ollama` role is one more process in `processes`.
  `GET /llm` is the same object alone, 200 when ready and 503 otherwise (404: no model server),
  for health checks such as the queue-health dependency watcher.
- Pause / resume use launchd directly (`launchctl kill TERM` / `kickstart` on
  `gui/<uid>/com.hamzalabs.maktaba.server`), no server endpoint.
