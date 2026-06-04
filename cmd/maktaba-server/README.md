# maktaba-server

The unified Maktaba binary: one Go executable that supervises the API,
streaming, and pipeline services, serves the embedded web UI, and
carries every CLI subcommand an installer needs.

## Architecture

`maktaba-server` owns **lifecycle and configuration**, not service logic.
The three services already live in their own modules (`api/`,
`streaming/`, Python `pipeline/`) and are fully driven by environment
variables. This binary:

1. Reads a single human-facing `server.toml`.
2. Translates it into the per-service environment each role already
   understands (`internal/supervisor/env.go`).
3. Launches the role binaries as **managed child processes** and owns
   their graceful shutdown (`internal/supervisor`).
4. Serves the embedded SPA in-process and reverse-proxies `/api` to the
   API service, so the whole app is reachable from one origin.

Because the role binaries stay unchanged, their existing test suites and
CI gates are untouched.

```
maktaba-server serve
  ├─ web      (in-process: embedded SPA + /api reverse proxy)
  ├─ api      → exec maktaba-api serve
  ├─ streaming→ exec maktaba-streaming serve
  └─ pipeline → exec python -m maktaba_pipeline
```

## Subcommands

| Command      | Purpose                                                        |
|--------------|----------------------------------------------------------------|
| `serve`      | Start all roles (or one with `--role api\|streaming\|pipeline\|web`) |
| `setup`      | Interactive first-run wizard → writes `server.toml`, migrates  |
| `update`     | Self-update from the release manifest (`--check` for dry-run)  |
| `uninstall`  | Guided teardown (`--purge` for non-interactive full removal)   |
| `models`     | Whisper model management (`list`, `download <name>`, `active`) |
| `migrate`    | Database migrations (delegates to `maktaba-api`)               |
| `adduser`    | Seed the first admin user (delegates to `maktaba-api`)         |
| `keys`       | JWT signing-key management (delegates to `maktaba-api`)        |

## Building

```sh
make server        # production: builds the SPA and embeds it (-tags embed_web)
make server-dev    # dev: skips the embed for a faster compile
```

The embedded UI is gated behind the `embed_web` build tag so dev builds
compile without reading every file under `web/dist`. `make server` copies
`web/dist` into `internal/webui/dist` before building; those copied
assets are git-ignored (only `.gitkeep` is tracked).

## Storage backends

The `[database].url` scheme selects the driver (`internal/dsn`):

- `postgres://…` → lib/pq (owned by the api binary)
- `sqlite://…`   → modernc.org/sqlite (pure-Go, CGO-free)

The migration tree already ships `.sqlite.sql` parity siblings, so the
same `migrate` command works against either backend.
