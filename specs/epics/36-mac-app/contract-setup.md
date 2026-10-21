# Contract — `maktaba-server setup --json` (Epic 36, story 36.6)

> **Owner of this contract:** track C (the app shell asks for it). **Implements it:** track A
> (`cmd/maktaba-server`). **Consumer:** `apps/desktop/src-tauri/src/setup.rs`, which runs it and
> relays it to the setup screens in `web/src/pages/Setup/`. Until track A lands it, the app's tests
> run a stub script that speaks this contract.

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
  Each step saves what it collected in the pending setup state; `finish` applies it.
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
Input `{"username": "admin", "password": "…"}`. Errors: `weak_password`, `bad_username` (with `field`).
Result `{"username": "admin"}`.

### `libraries` — library folders and the NAS path map
Input:
```json
{"roots": ["/Volumes/Media/Lectures"],
 "path_map": [{"from": "/mnt/media", "to": "/Volumes/Media"}]}
```
`path_map` rewrites paths other machines report (`MAKTABA_PATH_MAP`); it may be empty.
Result: `{"roots": [{"path": "/Volumes/Media/Lectures", "exists": true, "readable": true}]}`.
Error `root_missing` / `root_unreadable` with `field: "roots"` and the path in `message`.

### `models` — transcription models
Input, one of:
```json
{"source": "huggingface", "hf_token": "hf_…"}
{"source": "nas", "path": "/Volumes/Models/huggingface"}
```
The Cohere second-listen model is gated: with `huggingface` the token is checked, saved under
`keys/` (mode 0600) and the models are downloaded, streaming `progress` with `stage: "download"`,
byte `done`/`total`. With `nas` the copy at `path` is verified and used in place. Errors:
`hf_token_invalid`, `hf_gated` (the user must accept the model's terms on huggingface.co),
`nas_missing`, `disk_full`. Result `{"models": [{"name": "…", "path": "…"}]}`.

### `plex` — optional Plex sign-in (PIN flow; the Plex password never reaches Maktaba)
Input `{"action": "skip"}` → result `{"linked": false}`.
Input `{"action": "pin"}` → result `{"pin_id": 123, "code": "ABCD", "link_url": "https://plex.tv/link"}`;
the screen shows the code and opens `link_url` in the browser.
Input `{"action": "poll", "pin_id": 123}` → result `{"linked": true}` once the user entered the code
(the server stores the Plex token in `keys/`), `{"linked": false}` while waiting; error `pin_expired`.

### `venv` — the pipeline's Python environment (story 36.3)
Input `{}`. Builds (or checks) the pipeline venv and streams its progress as `progress` events with
`stage` from track B's bootstrap (see `contract-venv.md`), `done`/`total` when known. Result
`{"python": "3.12.x", "pipeline": "<version>", "reused": false}`. Error `venv_failed` (the message
names the package that failed; the full log is in `~/Library/Logs/Maktaba/venv.log`).
Track A relays track B's bootstrap here, so the app depends on this one contract only.

### `finish`
Input `{}`. Writes `server.toml`, creates the database, runs migrations, seeds the admin account,
and returns `{"web_url": "http://127.0.0.1:8088"}`. After it the app registers the login item
(which starts the server), waits for `/processes` to show every role running, and opens `web_url`.

## Not in the contract

- The supervisor's state is the existing `GET http://127.0.0.1:9103/processes` (`supervisor.Snapshot`).
- Pause / resume use launchd directly (`launchctl kill TERM` / `kickstart` on
  `gui/<uid>/com.hamzalabs.maktaba.server`), no server endpoint.
