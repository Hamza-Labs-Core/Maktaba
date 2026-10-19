# Plan — The cluster channel (Epic 31, phase 2)

Design: [design-agent.md](design-agent.md) (section numbers below, `§n`, refer to it).
Phase 1: [design.md](design.md), [plan.md](plan.md). User documentation:
[docs/cluster](../../../docs/cluster/README.md).

Branch: `feat/cluster-agent` (integration branch; one PR into `main` at the end). Each track
works in its own worktree on its own branch cut from `feat/cluster-agent` and is merged back
by the integration step.

## Order

```
F   foundation: design-agent.md, this plan, docs/cluster, agent_vectors.json   ── done first
├─ T4  hub (Go, api/)                                   ┐
├─ C1  database cache (Go, api/internal/cluster/cache)  │
├─ T5  agent (Python, pipeline/)                        │
├─ T6  web (React, web/)                                ├─ in parallel, each in its own worktree
├─ H1  end-to-end harness (tools/dev/)                  │
└─ P1  worker service packaging (deploy/)               ┘
I   integration: merge T4 → C1 → T5 → T6 → H1 → P1, gates, end-to-end checklist, review, one PR
```

T4 and T5 meet on the wire protocol; both start by writing their shared fixtures (below)
from the design, so neither waits for the other's code.

## Contracts between tracks

| Contract | Source of truth | Producer | Consumers | Fixture (written first, by the producer) |
|---|---|---|---|---|
| K1 Agent handshake (version 2) and sealing | §4.2–§4.4 | — | T4, T5 | `shared/testdata/cluster/agent_vectors.json` (version 2, both variants; exists; do not change) |
| K1a The home's cluster key at join | §4.2 | T4 (sealed payload `home.cluster_pub`, 32 bytes base64) | T5 (node config) | none: T4's approve test and T5's join test both check the member (the phase 1 sealing vectors are not changed) |
| K1b Health numbers | §5.2 `health` (a flat map of today's metric names), §7.9 (a) | T5 | T4, C1 | in `agent_frames.json`: a `metrics` frame with `health`, and a `final` one |
| K2 Agent frames | §5 | T5 | T4 | `shared/testdata/cluster/agent_frames.json`: one plaintext example of every frame type and `job` event |
| K3 Close codes, limits, versioning | §4.5–§4.7 | T4 | T5 | in `agent_frames.json` (`close_codes` table) |
| K4 Browser socket messages and topics | §6.4 | T4 | T6 | `shared/testdata/cluster/hub_messages.json`: every browser→hub `op` and hub→browser `type`, one per topic |
| K5 Files on the share | §7.4–§7.6 | T5 | T4 (REST reader), T5 (CLI), H1 | `shared/testdata/cluster/files/`: a small cluster directory (two IP folders, every kind, two days, a partial last line, a capped file) |
| K6 Share root, path map, IP folder | §7.1–§7.3 | T5 | T4 | `shared/testdata/cluster/share_root_cases.json`: library roots → share root; path maps → local cluster directory per OS |
| K7 LLM wire | §8.2–§8.4 | T4 (door, router), T5 (executor) | H1 | none shared; each side tests against the OpenAI chunk format |
| K8 REST for the web | §6.7 | T4 | T6 | `hub_messages.json` carries a sample `GET /api/cluster/processes` and a `files` listing |
| K9 Process behaviour for service managers | below (P1) | T5 | P1 | — |
| K10 The database cache | §7.9 | C1 | T4 | the Go interfaces of `api/internal/cluster/cache` (below), committed first with a fake for T4's tests |

**K9.** `maktaba-pipeline` is an installed command (console script, added by T5). `run`
exits with code 0 after a revocation (so "restart on failure" does not loop) and with code 2
when it is neither given a database URL nor joined; any other stop on error is non-zero.
`MAKTABA_NODE_CONFIG` selects the node config file. SIGTERM (Windows: CTRL_BREAK / service
stop) drains and exits as today.

**K10.** The cache package exposes three things to the hub:

- `Sink` — `Metrics(proc, frame, health, final)` for every `metrics` frame (with the
  `health` map and whether it is the process's last), `Logs(proc, records)` for every
  `logs` batch. Never blocks: a full or failing database drops and counts.
- `Reader` — `Covers` (does the cache hold this IP and range?), `ReadMetrics`, `ReadLogs`
  (the same records as the files).
- `Run` (writers and the 10-minute prune), `Backfill` (refill from the files at start), and
  `Stats` (written, dropped, oldest and newest row — shown as `cache` in the `processes`
  snapshot).

Fixtures are reviewed against the design by the integration step; a fixture change after
day one is announced to the consuming tracks.

## T4 — Hub (Go, `api/`)

**Scope.** The agent endpoint and protocol, the hub's live state and fan-out, the browser
WebSocket, commands and revocation, the home's own files and the REST readers, the share
root and IP rule in Go, the LLM router and the OpenAI-compatible door, the home's cluster
key, and feeding and reading the database cache through track C1's interfaces (K10).

**Owned files.**

- `api/internal/cluster/agent/` — handshake version 2 (node and home kinds), transcript hash, key derivation, both proofs, sealing
  with counters, frame types, close codes, limits; a Go test client (`agent/agenttest`) that
  speaks the protocol for tests.
- `api/internal/cluster/hub/` — sessions, live state, `replaced`, revocation, commands,
  `reload_stages` triggers, topics and per-browser queues, the queue snapshot loop (reusing
  `queues.Handler.BuildSnapshot`), the library snapshot.
- `api/internal/cluster/clusterfiles/` — share root rule, `MAKTABA_PATH_MAP` parsing (the
  `--path-map` format), IP rule, the hub's writers (`events`, `queues`, `library`),
  retention sweep, the reader (merge by `ts`, partial last line) for REST.
- `api/internal/cluster/llmrouter/` — runtime registry keyed by (machine, provider, base
  URL), selection, FIFO per model with timeout, id mapping, cancel, `machine_gone`.
- `api/internal/handlers/clusterhub/` — `GET /api/cluster/agent`, `GET /api/cluster/ws`,
  `GET /api/cluster/processes`, `POST /api/cluster/processes/{session}/cmd`,
  `GET /api/cluster/files[/{ip}/{kind}]`, `/api/cluster/llm/v1/{models,chat/completions,embeddings}`.
- Wiring: `api/internal/router/p31.go`, `api/main.go`, `api/cluster_bootstrap.go` (home
  agent key, home cluster key, settings), `api/internal/auth/middleware/middleware.go`
  (allowlist `/api/cluster/agent` only) and its allowlist test.
- The phase 1 sealed payload (`api/internal/cluster/payload.go`, the approve handler):
  `home.cluster_pub`.
- Cache wiring (K10): sessions hand every `metrics` frame and `logs` batch to `cache.Sink`;
  the files endpoint asks `cache.Reader` for `metrics` and `logs` and sets
  `X-Maktaba-Source`; the `processes` snapshot carries `cache.Stats()`; `main.go` starts
  `Run` and `Backfill`. The health readers (`queues/health.go`, `rate.go`,
  `transcription.go`) are not changed.
- Phase 1 hooks in `api/internal/handlers/cluster/nodes.go`: revoke → `hub.Revoke(node)`;
  switch or drain change → `hub.ReloadStages(node)`.
- `api/go.mod` / `go.sum`: `github.com/gorilla/websocket` v1.5.3 (as `cloud/` uses).
- Fixtures K4, K8.

**Tasks.**

1. Agent protocol package, version 2; vector test (both variants: DH results, transcript
   hash, keys, both proofs, the three frames).
2. Home keys, created at first start (0600, local): the agent key
   (`~/.maktaba/cluster/home-agent.key` or `MAKTABA_HOME_AGENT_KEY`) and the cluster key
   pair (`~/.maktaba/cluster/home-cluster.key` or `MAKTABA_HOME_CLUSTER_KEY`); the public half
   added to the sealed join payload as `home.cluster_pub`.
3. Agent endpoint: upgrade, 10 s handshake deadline, rate limit 30/min per address, node
   lookup through the phase 1 store, `welcome.settings`, ping/pong, close codes.
4. Hub: live state per §6.1, sessions per worker id, revocation per §6.2, commands per §6.3.
5. Browser socket per §6.4: auth and `Origin`, topic access, snapshots, envelopes, slow
   browser policy; queue snapshot pushed on change at most once a second.
6. Files: share root (resolved once a minute, sent in `welcome`), IP folder, hub writers,
   retention sweep, REST readers with strict validation (`filepath.Clean` and a within-base
   check at the sink — the shape CodeQL recognises).
7. LLM router and the OpenAI door (SSE in the OpenAI format; error mapping of §8.4);
   admins and personal access tokens only, 403 for an ordinary user's session.
8. Cache wiring of K10 (§7.9 (d), (e)).
9. Settings of §9 read from the environment.

**Tests.** Vectors (version 2, both variants); handshake negatives (unknown, pending,
revoked → 4003; wrong client proof, a changed byte in `hello` or `challenge`, an all-zero DH
result → 4004; a test client holding the wrong home cluster key rejects `welcome`; slow
handshake → 4008; `v: 1` or unknown version → 4001; text after welcome → 4000; counter gap,
repeat, bad tag → 4011; oversize → 4012; rate limit → 4013); `replaced`; revocation closes
every session of a node within 1 s and refuses reconnects; commands round-trip with
timeout; browser topics (signed-in vs admin, `Origin`), snapshots, `dropped` for a slow
reader; share root cases (K6) and IP rule; writer one-file-per-writer, daily rotation with a
fake clock, cap, retention; reader merge and partial line (K5); router selection order,
FIFO fairness, `no_model`, `no_capacity` timeout, `machine_gone`, cancel on requester close;
OpenAI door streaming and non-streaming, 403 for an ordinary session, 200 for a personal
access token and for an admin; the files endpoint answers from a fake `Reader` when it covers
the range (`X-Maktaba-Source: cache`) and from the files otherwise (`files`), with the same
JSON; a blocked or failing `Sink` never slows a session. All with `-race`. Postgres-dependent tests skip
when Postgres is unavailable, as phase 1's do.

**Acceptance.** The Go test client, using the vector keys, completes a handshake and
exchanges sealed frames that decode to the vectors' plaintexts; a fake agent's `job` frames
reach a subscribed browser test client in order; revocation closes the sessions with 4010;
the REST reader serves the K5 tree merged by `ts`; a router test with two fake machines
routes, queues and fails exactly as §8.2 says; Go gates green, coverage floor held.

## C1 — Database cache (Go, `api/internal/cluster/cache`)

**Scope.** §7.9: the database as a cache of the channel, written only by the hub, bounded
and disposable.

**Owned files.**

- `api/internal/cluster/cache/` — `Sink`, `Reader`, `Run`, `Backfill`, `Stats` (K10), with a
  fake for T4's tests.
  - `health_samples` writer: each process's `health` at most every 10 s, `at` = the hub's
    clock, `host` = the process's host, one row per metric, the `final` frame always
    written; `nas.reads_busy` counted by the hub from the queue (jobs of the `nas_read`
    pool that are claimed or running), as workers counted it.
  - The 48-hour fold of raw `health_samples` into 10-minute rollups and the pruning of old
    rollups, moved here from the workers' pruner (`engine/prune.py`): with the migration
    below, a machine's worker may no longer write the table, so the hub does it.
  - `cluster_metrics_cache` (one frame per process per 10 s) and `cluster_log_cache` (every
    record): batched inserts, `MAKTABA_CLUSTER_CACHE_HOURS` (default 24; `0` disables them),
    pruning every 10 minutes.
  - Coverage per IP: never claim a range not held; at start `Backfill` refills the whole
    window when the tables are empty, else the gap since the newest row; idempotent and
    symlink-safe (links out of the cluster directory are skipped).
  - Drop and count when the database is slow or down; never block the caller.
- `shared/db/migrations/0119_cluster_cache.sql`, `0119_cluster_cache.sqlite.sql` and the
  MANIFEST row: the two `UNLOGGED` tables and their indexes (plain tables in SQLite);
  `REVOKE ALL` on both from `maktaba_worker` (default privileges would grant them);
  `REVOKE INSERT, UPDATE, DELETE ON health_samples FROM maktaba_worker`. Slot 0119 (first
  written as 0104; PRs 57 and 59 took 0104–0118 and merged first).
- A Postgres role test beside phase 1's (`api/internal/cluster/pg_integration_test.go` or a
  new file in the package): `maktaba_worker` cannot insert into, update or delete from
  `health_samples` and cannot read or write the cache tables.

**Tests.** Writer cadence (at most one row set per process per 10 s; the `final` frame
always written); rows shaped exactly as today's, so the existing health and transcription
contract tests pass reading them; `nas.reads_busy`; the rollup fold and pruning with a fake
clock; cache retention and `0` disabling; coverage (an uncovered range is never claimed;
gap refill; empty tables → whole window); backfill twice → the same rows; a symlink out of
the directory skipped; a blocked database → dropped and counted, the caller not slowed;
the role test; the SQLite sibling. Skip Postgres-only tests without Postgres, as phase 1's do.

**Acceptance.** Fed the K1b frames, the cache writes `health_samples` rows that
`GET /api/queues/health` and `GET /api/transcription/summary` read unchanged; after the cache
tables are truncated, a start refills them from the K5 files tree and `Covers` says so;
`maktaba_worker` is denied by the database; Go gates green, migration lint green.

## T5 — Agent (Python, `pipeline/`)

**Scope.** The agent in every worker process (both kinds), metrics with the health numbers,
log shipping, job events, commands, the files on the share and their CLI readers, the LLM
runtimes, executor and `cluster_llm()`, the home's cluster key in the node config.

**Owned files.**

- `pipeline/src/maktaba_pipeline/cluster/channel/` — `crypto.py` (HKDF, HMAC, sealing with
  counters, on `cryptography`), `protocol.py` (frames, close codes, limits), `client.py`
  (connect, handshake, ping, reconnect with backoff and jitter), `agent.py`
  (`ChannelAgent`; the name `WorkerAgent` is taken by phase 1's `cluster/node.py`).
- `pipeline/src/maktaba_pipeline/cluster/metrics.py` — system, GPU (NVIDIA via `pynvml` when
  present, Apple via MLX when present), process, pipeline, LLM and `health` sections of §5.2
  (`health` from the existing collectors of `engine/health.py`).
- `engine/health.py`: `measure()` split out of `sample_once()` — it returns the flat map
  (today's metric names, without `nas.reads_busy`) for the `health` section; workers no
  longer write `health_samples` at all; the last `metrics` frame at a graceful stop carries
  the final values (as the final sample does today).
- `engine/prune.py`: the workers' pruner no longer touches `health_samples` (the hub folds
  and prunes it, track C1).
- `cluster/config.py` and `cluster/join.py`: `home.cluster_pub` read from the sealed payload
  and kept in the node config.
- `pipeline/src/maktaba_pipeline/cluster/files.py` (share root, IP folder, writers, caps,
  retention, `processes/*.json`, local fallback) and `cluster/reader.py` (merge, follow).
- `pipeline/src/maktaba_pipeline/cluster/cli.py` — `maktaba-pipeline cluster where|ls|logs|metrics|jobs|llm|events|queues|library`.
- `pipeline/src/maktaba_pipeline/llm/` — runtime discovery (Ollama, OpenAI-compatible),
  executor (max parallel, cancel, usage record), `cluster_llm()`.
- Hooks: a structlog processor feeding the shipper and the log file
  (`maktaba_pipeline/log/`); job events from the engine (`engine/dispatch.py`,
  `engine/hooks.py`); `reload_stages` → the phase 1 `StageSelector` refresh
  (`cluster/node.py`); `drain`; revocation by `disconnect` → the phase 1 graceful stop.
- `runtime.py` / `__main__.py`: start the agent (unless `MAKTABA_CLUSTER_AGENT=0`); kind
  `home` (home agent key, `MAKTABA_API_URL`) or `node` (node config); the `cluster`
  subcommand; exit codes of K9.
- Scanner and watcher skip `<share root>/maktaba/`.
- `pipeline/pyproject.toml`, `uv.lock`: base dependencies `websockets`, `psutil`; console
  script `maktaba-pipeline = "maktaba_pipeline.__main__:main"`.
- The Windows unit job (`.github/workflows/_unit.yml`) runs `tests/cluster` including the
  new tests.
- Fixtures K2, K5, K6.

**Tasks.**

1. Channel crypto and protocol, version 2; vector test (both variants).
2. Client and `ChannelAgent`: handshake version 2 for both kinds (exact `hello` and
   `challenge` bytes kept for the transcript; the hub's proof checked before any sealed frame;
   "this is not the home I joined" on failure; no connection and "re-join this machine" once
   an hour without `home.cluster_pub`), sealed frames, ping, backoff
   (1 s → 30 s, ±20 %, reset after 60 s), close code handling of §4.6, buffers of §5.5.
3. Metrics every `metrics_every` seconds with the flat `health` map, and a last frame at a
   graceful stop; workers no longer write `health_samples` (`measure()` split); the pruner
   leaves `health_samples` to the hub; log shipping (batched within 250 ms, level from
   `welcome` and `cmd log_level`); `job` frames for every event (progress at most 1/s).
4. Files: share root (from `welcome`, else computed from the database), path map, IP
   folder, writers per kind and day (UTC), flush every 2 s off the worker's path, caps,
   `processes/*.json` refresh, retention, local fallback with 5-minute re-check, `files`
   section in metrics.
5. CLI readers.
6. LLM: discovery every 30 s and `llm.models`; executor for `llm.request` (OpenAI bodies to
   the runtime's `/v1` endpoints, chunks passed through, cancel); `cluster_llm()` local-first,
   else over the channel; usage lines in the `llm` file, no text.
7. Commands of §5.4.
8. Dependencies, console script, scanner and watcher exclusion.

**Tests.** Vectors (version 2, both variants); handshake and frames against a small in-test
fake hub (server side of the protocol, test-only); a fake hub with the wrong cluster key is
refused before any sealed frame is sent ("this is not the home I joined"); a node config
without `home.cluster_pub` makes no connection and logs "re-join this machine"; `join`
stores `home.cluster_pub`; every close code's reaction; `health` shape (K1b, flat, today's
names), a last `metrics` frame at a graceful stop, no `health_samples` write from any worker
(with or without the agent), the pruner leaving `health_samples` alone; backoff timing with a fake clock;
buffer limits and `dropped`; metrics shape against K2; log level changes; job events from a
fake job run; files: one writer per file, rotation at UTC midnight (fake clock), caps,
partial-line reader, retention, fallback and return (temp dirs as the share), share root
and IP cases (K6, monkeypatched sockets), Windows paths (drive letters, UNC) on the Windows
job; CLI output and `--follow`; LLM discovery against a fake Ollama and a fake
OpenAI-compatible server (in-test HTTP), executor parallel limit and cancel;
`cluster_llm()` local vs remote. `mypy --strict` (tests included), `ruff`, `pytest -m unit`,
both databases where the database is touched.

**Acceptance.** A worker started against the T4 test hub (in integration) or the fake hub
(here) connects, sends metrics every 2 s, ships logs at the session's level, reports every
job event, writes the K5 layout under a temp share, and stops without reconnecting after
`4010`; `maktaba-pipeline cluster logs --follow` prints lines from two processes merged by
time; Python gates green on Linux and the cluster tests green on Windows.

## T6 — Web (`web/`)

**Status.** Delivered and merged into `feat/cluster-agent` (merge b3847860); the file list
below is what was built.

**Scope.** One cluster socket for the app, the Processing page on the hub with the SSE
fallback, the Machines page's live views and history.

**Owned files.**

- `web/src/lib/clusterSocket.ts` — one WebSocket per app, ref-counted topic subscriptions,
  reconnect with backoff and re-subscribe, `socketFallback()` for callers with an older path,
  `cmd()` resolving on `cmd.result`, `dropped` surfaced; envelope `{type, topic?, data}`.
- `web/src/lib/clusterStream.ts` — the live cluster state for the Machines views (processes
  by session, a rolling 5-minute history per process, job and LLM feeds, queue and library
  samples, the log tail), as a pure reducer.
- `web/src/lib/clusterApi.ts` — types and REST additions of §6.7.
- `web/src/lib/useQueueStream.ts` — fed by the hub, or by the SSE streams in fallback; the
  same outputs for `QueueOverview` and `QueueDetail`.
- `web/src/pages/queue/*`, `web/src/components/queue/*` — running jobs on every machine with
  live progress and the machine's name; worker strip with machine names; a quiet fallback
  indicator.
- `web/src/pages/Admin/Machines.tsx` and `web/src/components/cluster/*` — per machine:
  processes, metrics charts, jobs, queues, library, log tail with level and follow, LLM
  runtimes and models with the try box (admin-only, through the OpenAI door), per-process
  commands; history from `/api/cluster/files/{ip}/{kind}`.
- `web/src/styles/cluster.css`, `web/src/i18n/en.json`, `ar.json` — every new string in English
  and Arabic.

**Tests (vitest, testing-library, a fake WebSocket).** Subscribe/unsubscribe ref counting;
reconnect re-subscribes and replaces snapshots; fallback after two failed opens and return
to the WebSocket; `cmd()` resolves and times out; Processing page renders lanes and running
jobs from hub messages (K4) and from SSE in fallback; Machines live views render metrics,
logs and LLM sections; admin-only sections hidden for a non-admin; `dropped` notice; Arabic
render; the existing a11y test extended.

**Acceptance.** With the K4 messages replayed through the fake socket, the Processing page
shows a job pulled on a machine from `claimed` to `done` with progress and the machine's
name, and the Machines page shows that machine's processes, metrics and log tail; with the
WebSocket refused, the Processing page works on SSE; web gates green.

## H1 — End-to-end harness (`tools/dev/`)

**Scope.** A one-host cluster for development and for the integration checklist: the home
(API, Postgres, a home worker) and two pretend machines with their own node configs, IP
folders and fake LLM runtimes, on a temporary share.

**Owned files.**

- `tools/dev/cluster.sh` — `start|stop|status|join|approve`: uses the dev database (as
  `tools/dev/stack.sh` does), creates a temporary share root with a tiny library (a few
  short generated videos), starts the API with `MAKTABA_SHARE_ROOT` and `MAKTABA_ADMIN_TOKEN`,
  a home worker, and machines `m1`, `m2` (each: its own `MAKTABA_NODE_CONFIG`,
  `MAKTABA_CLUSTER_IP=10.99.0.N`, `MAKTABA_OLLAMA_URL` to its own fake runtime); joins each
  machine by reading the printed code and approving it through the admin API.
- `tools/dev/fake_llm.py` — a stdlib HTTP server speaking enough Ollama (`/api/tags`,
  `/api/ps`) and OpenAI (`/v1/models`, `/v1/chat/completions` streaming and not,
  `/v1/embeddings`) to exercise routing; a model list and a delay per instance.
- `tests/e2e/cluster/` — pytest scenarios driving the harness through the REST API, a
  WebSocket client on `/api/cluster/ws`, the files on the temporary share and the CLI.
- `Makefile` — `e2e-cluster` target.

**Tests.** The harness is the test; it must start and stop cleanly twice in a row on macOS
and Linux and leave nothing running.

**Acceptance.** `make e2e-cluster` runs the integration checklist below end to end in under
five minutes on a developer machine.

## P1 — Worker service packaging (`deploy/packaging/`)

**Scope.** Installing the pipeline on a machine and running it as a service on macOS,
Linux and Windows, with its node config where a service can read it and the share mounted
before it starts. (The home server's own packaging is unchanged.)

**Owned files.**

- `deploy/packaging/worker/README.md` — what each file is, how it is installed.
- `deploy/packaging/worker/install-worker.sh` (macOS, Linux) and
  `deploy/packaging/worker/install-worker.ps1` (Windows): install `uv` when missing, install
  `maktaba-pipeline` from the release (with the extras asked for), create the service, print
  the next step (`join`).
- `deploy/packaging/worker/systemd/maktaba-worker.service` — runs `maktaba-pipeline run` as
  a dedicated user; `After=network-online.target remote-fs.target`, a drop-in for
  `RequiresMountsFor=<share mount>`; the share mount writable (unlike the server unit's
  `ProtectSystem=strict` set); `Environment=MAKTABA_NODE_CONFIG=/etc/maktaba/node.json`;
  `Restart=on-failure`.
- `deploy/packaging/worker/launchd/com.maktaba.worker.plist` — a LaunchDaemon with
  `KeepAlive` on unsuccessful exit only and `MAKTABA_NODE_CONFIG` set.
- `deploy/packaging/worker/windows/` — a startup scheduled task (native, nothing extra to
  download) running as a chosen account, restart on failure, `MAKTABA_NODE_CONFIG` under
  `C:\ProgramData\Maktaba\`, access cut to that account; UNC paths recommended (mapped
  drive letters are not visible to services).
- `.github/workflows/release.yml` — build and publish the `maktaba-pipeline` wheel with the
  other release assets; a new `.github/workflows/_packaging.yml` (shellcheck, `plutil -lint`,
  `systemd-analyze verify`, PowerShell parse check) wired into `ci.yml` by the integration
  step.

**Tests.** The lint job above; the Linux installer run in a container against a wheel built
from the branch, ending with `maktaba-pipeline --version` and `systemd-analyze verify`; the
Windows script parsed on `windows-latest`.

**Acceptance.** On each OS the README's steps install the worker, `join` as the service's
account writes the config the service reads, the service starts after the share is mounted,
survives a reboot, does not restart after a revocation (K9), and stops cleanly.

## I — Integration

1. Merge T4, then C1, then T5 (run T5's channel tests against the real T4 endpoint, not
   only the fake hub), then T6, H1, P1 into `feat/cluster-agent`.
2. Gates: Go (lint, unit with `-race`), Python (ruff, mypy strict, unit on both
   databases), web (lint, typecheck, vitest), the Windows cluster job, packaging lint,
   migration lint (one migration: `0119_cluster_cache`),
   coverage floors, CodeQL (new alerts on
   the REST file readers and the LLM door are fixed, not dismissed, unless false positives).
3. Update [docs/cluster](../../../docs/cluster/README.md) with what the tracks changed (P1's
   service steps replace the generic ones in `join-a-machine.md`).
4. Run the checklist below with H1, then on real machines (the Mac home, one Linux, one
   Windows machine on the NAS).
5. Review (code-review skill), fix, one PR.

### Integration and end-to-end checklist

Connection and security

- [ ] A machine's worker and a home worker both connect; the hub lists them within 5 s.
- [ ] A wrong pairing key gets 4004; an unapproved machine 4003; a tampered frame 4011.
- [ ] With the home's cluster key file moved aside and the API restarted, every machine
      refuses the hub ("this is not the home I joined") and keeps pulling jobs; with the
      file put back, they reconnect.
- [ ] A machine whose node config has no `home.cluster_pub` does not connect, logs
      "re-join this machine", keeps pulling jobs; after revoke, remove and join again it
      connects.
- [ ] Revoking a machine closes all its sessions within 1 s; its worker stops and does not
      come back; its service does not restart it.
- [ ] Stopping the API: workers keep pulling and writing files; on restart everything
      reconnects and the live view is whole within 5 s; `dropped` is reported when logs
      overflowed.

Queues and the Processing page

- [ ] A job pulled on a machine shows on the Processing page from `claimed` to `done` with
      live progress and the machine's name; the queue lanes follow within a second.
- [ ] Switching a queue off for a machine on the Machines page stops its processes pulling
      from it within 2 s; switching it back resumes.
- [ ] Draining a machine and draining one process both work and are shown.
- [ ] With WebSockets blocked, the Processing page works on the SSE streams and returns to
      the WebSocket when it is allowed again.

Files on the share

- [ ] Every process writes `processes/`, `logs/`, `metrics/`, `jobs/` (and `llm/` when it ran
      a request) under its IP folder; the hub writes `events/`, `queues/` (every 10 s),
      `library/` (every 5 min) under the home's.
- [ ] `maktaba-pipeline cluster logs --ip <other machine> --follow` on a machine shows the
      other's records; `cluster where` reports the right local path on macOS, Linux and
      Windows (drive letter and UNC).
- [ ] With the share unreachable a process falls back to its local folder, reports it in
      its metrics, and moves back when the share returns.
- [ ] Retention removes expired days; a capped file stops growing and the drop is counted.

LLMs

- [ ] A model on machine B only: a request from machine A streams through the hub and
      completes; B's `llm` file has a usage line with no text.
- [ ] `curl` with a personal access token on `/api/cluster/llm/v1/chat/completions` streams;
      `/models` lists every model with its machines; no token → 401; an ordinary user's
      session → 403; an admin's session (the Machines page's try box) works.
- [ ] `no_model` at once; `no_capacity` after the timeout with the runtime busy;
      `machine_gone` when B stops mid-request; closing the client cancels B's work.

The database

- [ ] Only the cache migration (`0119_cluster_cache`); the database holds the channel only
      as the hub's cache (§7.9) — no LLM traffic, no settings; the queue engine's own
      `job_events` writes are unchanged.
- [ ] `health_samples` is written only by the hub: rows from every process at most every
      10 s, `host` the process's host; no worker writes it, and a machine's role is refused
      if it tries. The Processing page's health strip and sparklines, a queue's rate chart
      and the transcription report's live speed and time left show the same numbers as
      before; the 10-minute rollups keep forming.
- [ ] The cache tables hold the last `MAKTABA_CLUSTER_CACHE_HOURS` hours; history of
      metrics and logs answers with `X-Maktaba-Source: cache` inside that window and `files`
      beyond it, with the same records.
- [ ] Restarting the API keeps history (the cache survives); truncating the cache tables (or
      a database crash) and restarting refills them from the files.
- [ ] With the database stopped for a minute, the channel, the live views and the files carry
      on; the `processes` snapshot shows the cache's dropped count.
