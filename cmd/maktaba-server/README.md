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
   their graceful shutdown (`internal/supervisor`). Each child is watched
   on its own: a crash or a hang (failed `/healthz` probes) restarts only
   that child, with backoff and a crash-loop guard, and the state is
   served at `GET /processes` on `[supervisor] admin_addr` (default
   `127.0.0.1:9103`). The api shows it at `/api/system/processes`
   (Story 22.9; settings in `deploy/server.toml.example`).
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
| `setup`      | The desktop app's setup steps in the terminal (check, admin, libraries, Plex, finish) plus where the data goes and the database → writes `server.toml`, creates and migrates the database, seeds the admin |
| `update`     | Self-update from GitHub Releases, verified in process (Sigstore bundle, exact release-workflow identity, `release.json`, no downgrade); `--check`, `--version`, `--rollback`. In the Linux app's runtime copy with owner `headless`: installs the release's payload tarball as a new runtime and switches the service to it (an app-owned runtime is updated by the app) |
| `uninstall`  | Guided teardown (`--purge` for non-interactive full removal)   |
| `models`     | Whisper model management (`list`, `download <name>`, `active`); `models llm [model…] [--check]` downloads the language model(s) of the LLM stages into the model server (default `[llm] models`, `qwen2.5:7b`), showing the disk space first; resumable |
| `migrate`    | Database migrations (delegates to `maktaba-api`)               |
| `adduser`    | Seed the first admin user (delegates to `maktaba-api`)         |
| `keys`       | JWT signing-key management (delegates to `maktaba-api`)        |
| `migrate-from-dev` | Move a Mac running `tools/dev/stack.sh` + Docker Postgres into Maktaba.app (`--dry-run`, `--verify`, `--finalize`; [runbook](../../specs/epics/36-mac-app/migrate-from-dev.md)) |
| `setup --json --step <name>` | The desktop app's setup screens, one step per run: `status`, `check`, `admin`, `libraries`, `models`, `plex`, `venv`, `llm` (the language model, Epic 36 story 36.11), `finish` ([contract](../../specs/epics/36-mac-app/contract-setup.md)) |
| `service`    | The Linux app's systemd user unit: `install` (`--start`, `--replace`), `uninstall`, `start`, `stop`, `restart`, `linger [--off]`, `status`; `--json` for the app ([contract](../../specs/epics/39-linux-app/contract-runtime.md#5-maktaba-server-service--linux-a-systemd-user-unit)) |
| `runtime`    | The Linux app's runtime copy of the payload: `install` (`--from`, `--owner`, `--activate`; the first one becomes `current`), `activate <dir\|previous>`, `status`, `prune`; a version older than the newest this machine ran needs `--allow-downgrade`; `--json` for the app ([contract](../../specs/epics/39-linux-app/contract-runtime.md#3-maktaba-server-runtime-)) |

**The language model server (`[llm]`).** Maktaba.app bundles Ollama
(`Contents/Resources/server/ollama/`). A `serve` of the whole machine runs it as the
`ollama` role: `ollama serve` on `127.0.0.1:11533` (a free loopback port when another
program holds 11533; a user's own Ollama keeps 11434), its models in
`<state>/models/ollama`, its output in `~/Library/Logs/Maktaba/ollama.log`, restarted like
every role. Every role gets its address as `MAKTABA_OLLAMA_URL`; the pipeline uses it as
the model server of `rules.tagcheck` (check-tags, check-clips, enrich, display titles)
unless the Settings name one. The supervisor downloads a configured model it does not
have in the background, and serves the server's state in `/processes` (`llm`) and at
`GET /llm` (200 when every model is there, 503 otherwise).

```toml
[llm]
enabled = "auto"         # auto (when the payload carries ollama) | on | off
# binary = "/path/to/ollama"   # else $MAKTABA_OLLAMA_BIN, else the payload's
# port = 11533           # set: never moved
# url = "http://192.168.1.30:11434"   # an Ollama elsewhere; the bundled one does not run
models = ["qwen2.5:7b"]  # what the server keeps
pull = "auto"            # auto: the supervisor downloads a missing model | off
```

**Streaming on a cluster node (Epic 32.1).** On a machine that joined a home
(Epic 31, `node.json` holds its database credential), `maktaba-server serve
--role streaming` runs the streaming service as a cluster node: the home's
database through the node's own role, its node id, the path map from the
cluster; no extra flag. A node serves only the channel routes the home's front
door forwards (signed with the cluster stream key) and never advertises
HDHomeRun. It takes channels once the Live TV page's "Stream on this machine"
and the setting `tv_cluster.nodes_stream` are both on.

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

The migration tree already ships `.sqlite.sql` parity siblings and the same
`migrate up|status|version` command works against either backend. For `sqlite://`
DSNs it applies the twins itself (`api/migrate_sqlite.go`) instead of goose: stock
SQLite has no `ADD COLUMN IF NOT EXISTS`, which twelve legacy twins use, so the
runner turns it into a `PRAGMA table_info`-guarded `ADD COLUMN`, and it adds the
`videos` columns the 0004 rebuild needs before slot 0007 creates them. Old twins
are never edited. Rollbacks (`down`) are Postgres only.
