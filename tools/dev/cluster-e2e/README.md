# cluster-e2e: multi-machine Maktaba on one host

An end-to-end harness for multi-instance Maktaba (Epic 31,
[design](../../../specs/epics/31-multi-instance/design.md),
[cluster channel](../../../specs/epics/31-multi-instance/design-agent.md)). It builds a
scratch home and two machines that join it, on one host, drives them through the
cluster's life (join, approve, pull work, stage switches, revoke) and checks every step.
It never touches the live system.

```
tools/dev/cluster-e2e/run.sh            # set up, check, tear down; exit 0 = all passed
E2E_KEEP=1 tools/dev/cluster-e2e/run.sh # leave it all running to poke at
tools/dev/cluster-e2e/stop.sh RUN_DIR   # tear one run down (idempotent)
tools/dev/cluster-e2e/stop.sh --all     # tear down every run, and any stray scratch DB
```

A run takes about 1 m 45 s with phase 1 (most of it is the 30 s stage re-read, twice).

## What it builds

```
                       Postgres container (maktaba-postgres-1)
                       └─ cluster_e2e_<rid>   scratch database, every migration applied
                              ▲      ▲ own role          ▲ own role
            superuser DSN     │      │ maktaba_node_<A>  │ maktaba_node_<B>
 ┌── HOME ──────────────────┐ │   ┌── machine A ──────┐ ┌── machine B ────────────────┐
 │ maktaba-api 127.0.0.1:   │ │   │ pipeline run      │ │ pipeline run                │
 │   8190 (admin 9290)      │─┘   │ (node mode)       │ │ (node mode)                 │
 │ home worker (thumb_small)│     │ HOME=machine-a/   │ │ HOME=machine-b/home         │
 │ HOME=home/HOME           │     │ share at mnt/nas  │ │ share at ~/nas-nfs (symlink)│
 └──────────┬───────────────┘     └────────┬──────────┘ │ fake Ollama (its LLM)       │
            │                              │            └──────────┬──────────────────┘
            └──────────── nas/export  (the share; DB form /share/…) ┘
```

Everything lives in `$TMPDIR/maktaba-cluster-e2e/<rid>/` (`E2E_ROOT` moves it):

| piece | how |
|---|---|
| database | `CREATE DATABASE cluster_e2e_<rid>` via `docker exec … psql`; slot 0000 applied like the API does (outside goose), then `goose up` over a temp copy of the Postgres migration files (no `.sqlite.sql`) |
| instrumentation | a trigger in the scratch DB records every claim in `e2e_claims` (a finished job's `claimed_by` is cleared, so this is how the checks know who pulled what) |
| API | `go build` of `api/` into the run dir; loopback ports from 8190 / 9290 (the first free ones), random admin token, `MAKTABA_DISCOVERY_MDNS=0`, `MAKTABA_UPDATE_DISABLE=1`, its own `HOME` and data dir, `MAKTABA_CLUSTER_DIR=<share>/maktaba/cluster`, `MAKTABA_SHARE_ROOT=/share`, `MAKTABA_PATH_MAP=/share/=<share>/` |
| library | ten tiny clips made with `ffmpeg -f lavfi` (`testsrc`, mpeg4, no audio) in `nas/export/{videos,shows}`; two `libraries` rows (`/share/videos`, `/share/shows`) and their `videos` rows in database form (`/share/videos/clip-01.mp4`) |
| home worker | `maktaba_pipeline run --database-url <superuser DSN> --stages thumb_small`: it syncs the stage registry (a machine never does) and serves a home-only stage |
| machines | two `maktaba_pipeline` processes, each with its own `MAKTABA_NODE_CONFIG`, `HOME` and mount of the share: A through `MAKTABA_STORAGE_HINTS=<run>/machine-a/mnt/nas`, B through a `~/nas-nfs` symlink in its own HOME (macOS finds it on its own; elsewhere it is passed as a hint). Neither is given a path map: both infer it. |
| join | `join --home http://127.0.0.1:<port> --name e2e-<rid>-a`; the pairing code is read from its output, the admin API opens joins (`PUT /api/cluster/settings`), finds the pending request (`GET /api/cluster`) and approves it with the code (`POST /api/cluster/nodes/{id}/approve`); then `run` with no `--database-url` (node mode) and `--stages probe,thumb_small,review_pages,index` |
| fake Ollama | `fake_ollama.py` on a free port from 11500, for machine B only (`MAKTABA_OLLAMA_URL`); A and the home point at a closed port, so a real Ollama on this host is never used |
| python | the harness's own venv, `tools/dev/cluster-e2e/.venv` (`uv sync --all-extras --frozen` with `UV_PROJECT_ENVIRONMENT`), never the checkout's `pipeline/.venv` that a running stack uses |

Every process starts under `env -i` with an explicit environment, so nothing of the
caller's `MAKTABA_*` / `DATABASE_URL` leaks in, and with `HF_HUB_OFFLINE=1` (no model
downloads).

## What it checks

Phase 1 (join / approve / revoke, node mode):

1. **Registration**: `GET /api/workers` shows each machine's worker with `role=node`, its
   `node_name` and `node_id`; the home's worker with `role=home`; `GET /api/cluster` shows
   both machines approved and online.
2. **Path maps**: each machine inferred `/share/ -> <its own mount>/`
   (`path_map_source=inferred`), the two local paths differ, both storage checks read and
   write.
3. **Home-only queues**: although asked for with `--stages`, `thumb_small`,
   `review_pages` and `index` are off on both machines with reason `home_only`; ten
   `thumb_small` and two `review_pages` jobs are enqueued, and at the end the claims show
   the home's worker pulled them and no machine did; machines pulled from no queue they
   do not serve.
4. **Stage switches**: `PUT /api/cluster/nodes/{B} {"stages":{"probe":false}}`: B's
   worker drops `probe` (`stages_off.probe=switched_off`; latency printed), then five
   `probe` jobs all run on A (ffprobe through A's inferred map) and B claims none. Then
   B back to automatic and A off: five more, all on B.
5. **Revoke**: `POST /api/cluster/nodes/{A}/revoke`: A's process exits (printed: how long,
   and why, from its log), a login with A's credentials is refused, B keeps working.

Phase 2 (the cluster channel). Built in parallel, so each is **feature-detected** and
skipped with a message when its endpoint answers 404; `E2E_REQUIRE_PHASE2=1` makes a
missing endpoint a failure once phase 2 is merged.

- **Switch at once**: with an agent channel (`/api/cluster/agent`), a switch must reach
  the machine within `E2E_SWITCH_FAST` (10 s): the hub's `reload_stages`.
- **WebSocket hub**: `/api/cluster/ws` (admin bearer token) takes
  `{"op":"subscribe","topics":["queues","jobs"]}` and pushes `job` and `queues` messages
  while the second batch runs (a minimal stdlib WebSocket client; a message's kind is
  read from `type` / `t` / `op` / `kind` / `event`, or its `topic`).
- **Files on the share**: `<share>/maktaba/cluster/<ip>/{logs,metrics,jobs}/<date>/<worker>.jsonl`
  for every machine worker, and the hub's `queues/<date>/hub.jsonl`; a file found only in
  a machine's local fallback (`~/.maktaba/cluster`) is reported as such.
- **LLM routing**: `/api/cluster/llm/v1/models` lists B's model (`e2e-fake:latest`), and
  `POST /api/cluster/llm/v1/chat/completions` (plain, then streamed as SSE) answers with
  B's fake Ollama's reply (`cluster-e2e reply from e2e-<rid>-b`) and B's runtime logged
  the request. A has no runtime, so it cannot be the one serving it.

The output ends with a PASS / FAIL / SKIP table; the exit code is 1 when anything failed.

## Never touches the live system

- Its own database `cluster_e2e_<rid>`; never `maktaba`. Every `DROP DATABASE` goes
  through a `^cluster_e2e_[a-z0-9]+$` guard.
- Roles: the only roles it drops are the `maktaba_node_<32 hex>` roles its own machines
  were given (read from its database's `cluster_nodes` and its node configs), never by
  pattern. `maktaba_worker` (the group, shared with the live database) is left alone;
  its grants on the scratch database go with the database.
- Its own API on loopback ports (never 8080 / 9100 / 5182), mDNS off, update checks off.
- Its own `HOME` for the API and every pipeline process (thumbnails, caches, a phase-2
  home agent key, node configs all land in the run dir); a phase-2 home worker is pointed
  at the scratch API (`MAKTABA_API_URL`, `MAKTABA_HOME_URL`).
- Its own Python environment; `tools/dev/stack.sh` and the worktrees are not touched.

## Cleanup

`run.sh` cleans up on exit, success or failure (and on Ctrl-C / TERM): it kills the
run's processes (by pid, and anything whose command line names the run), drops the
scratch database (`WITH (FORCE)`) and its machine roles, and removes the run dir. On
failure it first prints the last lines of every log. `E2E_KEEP=1` keeps everything
(processes too) and prints the `stop.sh` command. `stop.sh` does the same cleanup for
named runs, or `--all` for every run plus any `cluster_e2e_*` database left in the
container (after a `kill -9` of `run.sh`, say). Both are idempotent.

## Prerequisites

- Docker with the dev Postgres container running: `maktaba-postgres-1` (the dev compose
  stack), user `maktaba` / `maktaba` on `localhost:5432`, a superuser (or `CREATEROLE`):
  approving a machine creates its role.
- `go` (builds `api/`), `goose` (`go install github.com/pressly/goose/v3/cmd/goose@latest`),
  `uv`, `ffmpeg` + `ffprobe`, `curl`, bash 3.2+.
- Warm Go module and uv caches, or network for the first run (`go build`,
  `uv sync --all-extras`).

## Settings

| variable | default | |
|---|---|---|
| `E2E_KEEP` | `0` | `1`: leave everything running |
| `E2E_ROOT` | `$TMPDIR/maktaba-cluster-e2e` | where run dirs go |
| `E2E_PG_CONTAINER` | `maktaba-postgres-1` | the Postgres container |
| `E2E_PG_USER` / `E2E_PG_PASSWORD` / `E2E_PG_HOST` / `E2E_PG_PORT` | `maktaba` / `maktaba` / `127.0.0.1` / `5432` | |
| `E2E_API_PORT` / `E2E_ADMIN_PORT` / `E2E_OLLAMA_PORT` | `8190` / `9290` / `11500` | first port tried; the next free one is used |
| `E2E_NODE_STAGES` | `probe,thumb_small,review_pages,index` | the machines' `--stages` |
| `E2E_HOME_STAGES` | `thumb_small` | the home worker's `--stages` |
| `E2E_VENV` | `tools/dev/cluster-e2e/.venv` | the harness's Python environment |
| `E2E_REQUIRE_PHASE2` | `0` | `1`: a missing phase-2 endpoint fails |
| `E2E_SWITCH_TIMEOUT` / `E2E_SWITCH_FAST` | `75` / `10` s | a switch must apply within; phase 2: at once within |
| `E2E_BATCH_TIMEOUT` / `E2E_REVOKE_TIMEOUT` / `E2E_FILES_TIMEOUT` / `E2E_LLM_TIMEOUT` | `120` / `60` / `45` / `75` s | |

## Files

| file | |
|---|---|
| `run.sh` | builds the cluster, runs `check.py`, cleans up |
| `stop.sh` | cleans up named runs, or `--all` |
| `lib.sh` | the shared cleanup and guards |
| `check.py` | the checks (pipeline venv: asyncpg; stdlib otherwise, including the WebSocket client) |
| `fake_ollama.py` | a stdlib fake Ollama: native `/api/*` and OpenAI-compatible `/v1/*`, streaming or not |

A run dir holds `logs/` (api, worker-home, node-a, node-b, join-a, join-b,
fake-ollama-b, migrate, build, venv), `context.json` (what `check.py` reads, including the
admin token), `state.env`, `pids`, the share and each machine's home.
