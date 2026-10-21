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

### Events

```jsonc
{"type":"progress","stage":"download","message":"Downloading whisper-large-v3-turbo","done":123,"total":456}
{"type":"result","data":{ /* step-specific */ }}
{"type":"error","code":"hf_gated","message":"Accept the model's terms on huggingface.co first","field":"hf_token"}
```

| Field | Meaning |
|---|---|
| `progress.stage` | short machine name of the current phase (`download`, `verify`, `venv`, …) |
| `progress.message` | one plain-English line for the screen |
| `progress.done` / `progress.total` | optional; bytes or items; `total` 0/absent → indeterminate bar |
| `error.code` | machine code; the app maps known codes to help text and shows `message` otherwise |
| `error.field` | optional; the input field the error belongs to (the screen highlights it) |

## Steps (in screen order)

### `status`
Input `{}`. Is this Mac already set up? The app calls it before showing setup: when `configured`,
it registers the login item again and opens `web_url` instead of a second setup (not after the user
chose "Connect to another Maktaba…").
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
  "warnings": ["Less than 32 GB of RAM: one transcription worker will run."],
  "runs": [
    {"role": "postgres", "what": "The library database (private to Maktaba)"},
    {"role": "api", "what": "The server the app and your devices talk to"},
    {"role": "streaming", "what": "Playback and Live TV"},
    {"role": "pipeline", "what": "Indexing, transcription (2 workers) and search"}
  ]
}
```
`ok: false` (not Apple Silicon, too little disk) blocks the Next button; `warnings` do not.

### `admin` — the first account
Input `{"username": "admin", "password": "…"}`. Errors: `weak_password` (8 to 256 characters),
`bad_username` (1 to 64 characters, no leading/trailing space or control character), with `field`.
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
Windows drive letter, a UNC path). Roots are cleaned (`Y:/` becomes `Y:` plus a backslash on
Windows) and kept once each.
Result: `{"roots": [{"path": "/Volumes/Media/Lectures", "exists": true, "readable": true}]}`.
Error `root_missing` / `root_unreadable` with `field: "roots"` and the path in `message`;
`bad_path_map` (a line without both paths).

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
accept the model's terms on huggingface.co), `nas_missing`, `network`. With the second listen off
(`[transcription] refine = "off"`) no token is needed.

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
short-lived server of its own; or a Postgres `[database] url`), runs the migrations
(`maktaba-api migrate up`), seeds the admin account from its hash (`maktaba-api adduser
--password-hash-file`, through a 0600 file removed after; an admin already there, exit 3, is not an
error), and, after a Plex sign-in, stores the Plex server (`maktaba-api plex-link`). Result
`{"web_url": "http://127.0.0.1:8088", "supervisor_url": "…", "plex": {"linked": true, "server":
"…"}}` (Plex is optional: a failure there is in the result, not an error). Errors: `admin_missing`,
`database_failed`, `database_unsupported`, `migrate_failed`, `admin_failed`. After it the app
registers the login item (which starts the server), waits for `/processes` to show every role
running, and opens `web_url`.

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
